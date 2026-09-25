package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type MailMode string

const (
	MailOff       MailMode = "off"
	MailDigest    MailMode = "digest"
	MailImmediate MailMode = "immediate"
	MailFrequent  MailMode = "frequent" // Explicit per-category choice; never a default.
)

var MailCategories = []string{"progress", "results", "tasks", "decisions", "deadlines", "receipts", "class_activity"}
var categoryLabels = map[string]string{
	"progress": "材料审核进展", "results": "材料认定结果", "tasks": "待办任务",
	"decisions": "重要结果", "deadlines": "截止提醒", "receipts": "操作回执", "class_activity": "班级处理进展",
}

type Preferences struct {
	Enabled    bool                `json:"enabled"`
	Categories map[string]MailMode `json:"categories"`
	DigestTime string              `json:"digestTime"`
	QuietStart string              `json:"quietStart"`
	QuietEnd   string              `json:"quietEnd"`
	DailyLimit int                 `json:"dailyLimit"`
}

func DefaultPreferences() Preferences {
	return Preferences{Enabled: true, Categories: map[string]MailMode{
		"progress": MailOff, "results": MailDigest, "tasks": MailDigest, "decisions": MailImmediate,
		"deadlines": MailImmediate, "receipts": MailOff, "class_activity": MailDigest,
	}, DigestTime: "18:30", QuietStart: "22:00", QuietEnd: "08:00", DailyLimit: 3}
}

func (p Preferences) Validate() error {
	if len(p.Categories) != len(MailCategories) {
		return errors.New("请完整设置邮件分类")
	}
	for _, category := range MailCategories {
		mode := p.Categories[category]
		if mode != MailOff && mode != MailDigest && mode != MailImmediate && mode != MailFrequent {
			return fmt.Errorf("%s的接收方式不正确", categoryLabels[category])
		}
	}
	for _, clock := range []string{p.DigestTime, p.QuietStart, p.QuietEnd} {
		if len(clock) != 5 {
			return errors.New("时间请使用 HH:mm 格式")
		}
		if _, err := time.Parse("15:04", clock); err != nil {
			return errors.New("时间请使用 HH:mm 格式")
		}
	}
	if p.DailyLimit < 1 || p.DailyLimit > 50 {
		return errors.New("业务邮件每日上限为 1—50 封，推荐 3 封")
	}
	return nil
}

func LoadPreferences(ctx context.Context, tx pgx.Tx, classID, userID int64) (Preferences, string, error) {
	p := DefaultPreferences()
	var raw []byte
	var token string
	err := tx.QueryRow(ctx, `SELECT settings,opt_out_token FROM mail_preference WHERE user_id=$1`, userID).Scan(&raw, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		var bytes [32]byte
		if _, err = rand.Read(bytes[:]); err != nil {
			return p, "", err
		}
		token = base64.RawURLEncoding.EncodeToString(bytes[:])
		raw, _ = json.Marshal(p)
		err = tx.QueryRow(ctx, `INSERT INTO mail_preference(class_id,user_id,settings,opt_out_token) VALUES($1,$2,$3,$4)
            ON CONFLICT(user_id) DO UPDATE SET user_id=EXCLUDED.user_id RETURNING settings,opt_out_token`, classID, userID, raw, token).Scan(&raw, &token)
	}
	if err != nil {
		return p, "", err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, "", err
	}
	return p, token, p.Validate()
}

func SavePreferences(ctx context.Context, tx pgx.Tx, classID, userID int64, p Preferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if _, _, err := LoadPreferences(ctx, tx, classID, userID); err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	if _, err := tx.Exec(ctx, `UPDATE mail_preference SET settings=$1,updated_at=now() WHERE user_id=$2`, raw, userID); err != nil {
		return err
	}
	off := []string{}
	for k, v := range p.Categories {
		if v == MailOff {
			off = append(off, k)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_notification SET status='suppressed' WHERE user_id=$1 AND status='pending' AND (NOT $2 OR category=ANY($3::text[]))`, userID, p.Enabled, off); err != nil {
		return err
	}
	for category, mode := range p.Categories {
		if mode == MailOff {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE mail_notification SET due_at=$3,mode=$4 WHERE user_id=$1 AND category=$2 AND status='pending'`, userID, category, p.Due(time.Now(), mode), mode); err != nil {
			return err
		}
	}
	return nil
}

var mailLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func atClock(day time.Time, clock string) time.Time {
	v, _ := time.Parse("15:04", clock)
	d := day.In(mailLocation)
	return time.Date(d.Year(), d.Month(), d.Day(), v.Hour(), v.Minute(), 0, 0, mailLocation)
}
func (p Preferences) afterQuiet(t time.Time) time.Time {
	if p.QuietStart == p.QuietEnd {
		return t
	}
	start, end := atClock(t, p.QuietStart), atClock(t, p.QuietEnd)
	if !end.After(start) {
		if t.Before(end) {
			return end
		}
		end = end.AddDate(0, 0, 1)
	}
	if !t.Before(start) && t.Before(end) {
		return end
	}
	return t
}
func (p Preferences) Due(now time.Time, mode MailMode) time.Time {
	due := now.Add(10 * time.Minute) // A burst of actions becomes one short email.
	if mode == MailFrequent {
		due = now
	}
	if mode == MailDigest {
		due = atClock(now, p.DigestTime)
		if !due.After(now) {
			due = due.AddDate(0, 0, 1)
		}
	}
	return p.afterQuiet(due)
}
