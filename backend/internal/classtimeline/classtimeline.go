// Package classtimeline reads and writes the per-class runtime envelope:
// the open/seal/lockdown instants, the parallel capability switches, and the
// honour-roll and scholarship policy.
//
// These used to live inside scheme.config, which made every deadline edit
// publish a new immutable scheme version. They are not scoring rules, so they
// now have their own mutable per-class row and the scheme keeps only weights
// and categories.
//
// The package sits between api and maintenance because both need to read the
// timeline, and neither may reach into the other. It deliberately stays thin:
// scheme owns the shapes and the validation, store owns the transaction.
package classtimeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/scheme"
)

// Timeline is the whole runtime envelope for one class.
//
// CollegeName and EnrollmentClass are report identity, not runtime policy: the
// 学院 forms want a college name and the 教务在线 class name, and neither is
// derivable from anything the system already stores (class.name is the short
// in-house name — "示例班级" — while 教务在线 calls the same class
// "24级计算机科学与技术2班", and 附件3 requires the latter). They live here
// because this is already the one mutable per-class settings row.
type Timeline struct {
	Window          scheme.Window
	Capabilities    scheme.Capabilities
	HonorRoll       scheme.HonorRoll
	CollegeName     string
	EnrollmentClass string
	AcademicYear    string
}

// Load returns the class's timeline, creating it from the system defaults on
// first read. The upsert-returning shape matches review_sla_config's handler:
// a class never has to be seeded ahead of time, and two concurrent readers
// cannot race into two different defaults.
//
// The transaction must already carry the tenant context, so the class id is
// taken from the caller rather than inferred.
func Load(ctx context.Context, tx pgx.Tx, classID int64) (Timeline, error) {
	defaultWindow := scheme.DefaultWindow()
	capabilitiesDefault, err := json.Marshal(scheme.DefaultCapabilities())
	if err != nil {
		return Timeline{}, err
	}
	honorRollDefault, err := json.Marshal(scheme.DefaultHonorRoll())
	if err != nil {
		return Timeline{}, err
	}

	var timeline Timeline
	var capabilitiesRaw, honorRollRaw []byte
	var publicityOpen, publicityClose *time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO class_timeline (class_id,open_at,close_at,capabilities,honor_roll)
		VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (class_id) DO UPDATE SET class_id=EXCLUDED.class_id
		 RETURNING open_at,close_at,lockdown_at,capabilities,honor_roll,
		           COALESCE(college_name,''),COALESCE(enrollment_class,''),academic_year,publicity_open_at,publicity_close_at
	`, classID, defaultWindow.Open, defaultWindow.Close, capabilitiesDefault, honorRollDefault).
		Scan(&timeline.Window.Open, &timeline.Window.Close, &timeline.Window.Lockdown, &capabilitiesRaw, &honorRollRaw,
			&timeline.CollegeName, &timeline.EnrollmentClass, &timeline.AcademicYear, &publicityOpen, &publicityClose)
	if err != nil {
		return Timeline{}, err
	}
	// PostgreSQL may decode timestamptz in the host time zone. Normalize before
	// JSON encoding, especially for the year-9999 open-ended default.
	timeline.Window.Open = timeline.Window.Open.UTC()
	timeline.Window.Close = timeline.Window.Close.UTC()
	if timeline.Window.Lockdown != nil {
		utc := timeline.Window.Lockdown.UTC()
		timeline.Window.Lockdown = &utc
	}
	if err := json.Unmarshal(capabilitiesRaw, &timeline.Capabilities); err != nil {
		return Timeline{}, err
	}
	if publicityOpen != nil && publicityClose != nil {
		timeline.Window.Publicity = &scheme.PublicityWindow{Open: publicityOpen.UTC(), Close: publicityClose.UTC()}
	}
	if err := json.Unmarshal(honorRollRaw, &timeline.HonorRoll); err != nil {
		return Timeline{}, err
	}
	return timeline, nil
}

// Lockdown reads only the freeze instant. The lockdown middleware runs on every
// business write, so it deliberately avoids decoding two JSONB columns it will
// not look at. A class without a row yet has never been configured and so is
// never locked.
func Lockdown(ctx context.Context, tx pgx.Tx, classID int64) (*time.Time, error) {
	var lockdown *time.Time
	err := tx.QueryRow(ctx, `SELECT lockdown_at FROM class_timeline WHERE class_id=$1`, classID).Scan(&lockdown)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return lockdown, nil
}

// Save replaces the whole envelope. Callers validate through
// scheme.ValidateTimeline first; the table's own CHECK constraints are the
// second line, not the first, so the caller can report a readable message.
func Save(ctx context.Context, tx pgx.Tx, classID int64, timeline Timeline) error {
	var publicityOpen, publicityClose *time.Time
	if p := timeline.Window.Publicity; p != nil {
		publicityOpen, publicityClose = &p.Open, &p.Close
	}
	capabilitiesRaw, err := json.Marshal(timeline.Capabilities)
	if err != nil {
		return err
	}
	honorRollRaw, err := json.Marshal(timeline.HonorRoll)
	if err != nil {
		return err
	}
	// 空串存 NULL：一个没填过学院名的班和一个把学院名清空的班是同一件事。
	_, err = tx.Exec(ctx, `
		INSERT INTO class_timeline (class_id,open_at,close_at,lockdown_at,capabilities,honor_roll,college_name,enrollment_class,academic_year,publicity_open_at,publicity_close_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11)
		 ON CONFLICT (class_id) DO UPDATE
		    SET open_at=EXCLUDED.open_at,close_at=EXCLUDED.close_at,lockdown_at=EXCLUDED.lockdown_at,
		        capabilities=EXCLUDED.capabilities,honor_roll=EXCLUDED.honor_roll,
		        college_name=EXCLUDED.college_name,enrollment_class=EXCLUDED.enrollment_class,
		        academic_year=EXCLUDED.academic_year,publicity_open_at=EXCLUDED.publicity_open_at,
		        publicity_close_at=EXCLUDED.publicity_close_at,updated_at=now()
	`, classID, timeline.Window.Open, timeline.Window.Close, timeline.Window.Lockdown, capabilitiesRaw, honorRollRaw,
		strings.TrimSpace(timeline.CollegeName), strings.TrimSpace(timeline.EnrollmentClass), timeline.AcademicYear, publicityOpen, publicityClose)
	return err
}

// Apply overwrites a scheme config's runtime fields with the class timeline.
// Every read path goes through here, so the rest of the codebase keeps using
// scheme.Config exactly as before and never learns where the envelope is
// stored.
func (t Timeline) Apply(config *scheme.Config) {
	config.Window = t.Window
	config.Capabilities = t.Capabilities
	config.HonorRoll = t.HonorRoll
}
