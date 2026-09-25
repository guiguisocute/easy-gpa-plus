package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/events"
	"easygpa/backend/internal/exportjob"
	"easygpa/backend/internal/objectstore"
	"easygpa/backend/internal/scheme"
	"easygpa/backend/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpTestTransport struct{ token string }

func (t mcpTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestMCPSecretAndRoleBoundaries(t *testing.T) {
	token, hash, err := mcpSecret()
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := mcpSecret()
	if !mcpTokenPattern.MatchString(token) || len(hash) != 32 || token == other {
		t.Fatal("invalid or repeated secret")
	}
	server := &Server{cfg: &config.Config{PublicURL: "https://gpa.example.org"}}
	for _, role := range []string{"student", "group", "class_admin", "ops", "unknown"} {
		for _, spec := range server.mcpToolRegistry() {
			conn := mcpConnection{Actor: Actor{Role: role}, Scopes: []string{"read", "draft", "submit", "review", "manage", "export"}}
			if mcpToolAllowed(conn, spec) && (role == "ops" || role == "unknown" || role == "student" && spec.Role != "student") {
				t.Fatalf("role %s leaked tool %s", role, spec.Name)
			}
		}
	}
	// Every schema must compile under the SDK, before a client can connect.
	for _, role := range []string{"student", "group", "class_admin"} {
		server.mcpTools = server.mcpToolRegistry()
		_ = server.newMCPProtocolServer(mcpConnection{Actor: Actor{Role: role, IsDeputy: true}, Scopes: []string{"read", "draft", "submit", "review", "manage", "export"}})
	}
}

func TestMCPConnectionLifecycleAndBusinessTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	opsConfig, err := pgxpool.ParseConfig(admin.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	opsConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE easygpa_ops")
		return err
	}
	opsPool, err := pgxpool.NewWithConfig(t.Context(), opsConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opsPool.Close)
	fx := seedSettlementClass(t, admin, false)
	other := seedSettlementClass(t, admin, false)
	password := "mcp-test-password-123"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET password_hash=$1 WHERE id IN ($2,$3)`, hash, fx.StudentID, fx.AdminID); err != nil {
		t.Fatal(err)
	}
	// Fixtures use an unrestricted synthetic local window, never production.
	caps, _ := json.Marshal(scheme.DefaultCapabilities())
	honors, _ := json.Marshal(scheme.DefaultHonorRoll())
	if _, err := admin.Exec(t.Context(), `INSERT INTO class_timeline(class_id,open_at,close_at,capabilities,honor_roll) VALUES($1,now()-interval '1 day',now()+interval '1 day',$2,$3) ON CONFLICT(class_id) DO UPDATE SET open_at=excluded.open_at,close_at=excluded.close_at`, fx.ClassID, caps, honors); err != nil {
		t.Fatal(err)
	}
	student := Actor{UserID: fx.StudentID, ClassID: fx.ClassID, Role: "student", TokenVersion: 1}
	owner := settlementActor(fx)
	owner.TokenVersion = 1
	server := &Server{cfg: &config.Config{PublicURL: "http://localhost", AppEnv: "dev", MCPMaxTTLHours: 24}, deps: Dependencies{Pools: &store.Pools{App: app, Ops: opsPool}}}
	router := gin.New()
	server.setupMCP(router)
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	server.cfg.PublicURL = httpServer.URL
	management := func(actor Actor, method, path string, input any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(input)
		return serveClassHTTP(t, app, actor, func(r *gin.Engine) {
			r.GET("/connections", server.myAgentConnections)
			r.POST("/connections", server.requireBusiness(), server.createAgentConnection)
			r.DELETE("/connections/:id", server.revokeAgentConnection)
			r.POST("/operations/:id/decision", server.decideAgentOperation)
		}, method, path, string(raw))
	}
	create := func(actor Actor, scopes []string) (string, string) {
		t.Helper()
		response := management(actor, "POST", "/connections", gin.H{"name": "Synthetic MCP client", "password": password, "ttlMinutes": 60, "scopes": scopes})
		if response.Code != 201 {
			t.Fatalf("create connection status=%d", response.Code)
		}
		var data struct{ ID, Token string }
		if json.Unmarshal(response.Body.Bytes(), &data) != nil || data.ID == "" || data.Token == "" {
			t.Fatal("missing one-time connection result")
		}
		return data.ID, data.Token
	}
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "EasyGPA regression CLI", Version: "1"}, nil)
		session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpTestTransport{token}}}, nil)
		if err != nil {
			t.Fatalf("MCP handshake: %v", err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	call := func(client *mcp.ClientSession, name string, args any, want int) map[string]any {
		t.Helper()
		response, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("tool %s failed: %v", name, err)
		}
		raw, _ := json.Marshal(response.StructuredContent)
		var envelope struct {
			Status int            `json:"status"`
			Data   map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Status != want || response.IsError != (want >= 400) {
			t.Fatalf("tool %s status=%d want=%d response=%s", name, envelope.Status, want, raw)
		}
		return envelope.Data
	}
	bad := management(student, "POST", "/connections", gin.H{"name": "Bad scope", "password": password, "ttlMinutes": 60, "scopes": []string{"manage"}})
	if bad.Code != 403 {
		t.Fatal("student granted admin scope")
	}
	bad = management(student, "POST", "/connections", gin.H{"name": "Bad password", "password": "wrong", "ttlMinutes": 60, "scopes": []string{"read"}})
	if bad.Code != 403 {
		t.Fatal("password recheck missing")
	}
	_, readToken := create(student, []string{"read"})
	readClient := connect(readToken)
	listed, err := readClient.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(listed.Tools, func(tool *mcp.Tool) bool { return tool.Name == "scores.mine" }) || slices.ContainsFunc(listed.Tools, func(tool *mcp.Tool) bool {
		return tool.Name == "operations.commit" || tool.Name == "submissions.draft" || strings.HasPrefix(tool.Name, "class.force")
	}) {
		t.Fatal("tools/list leaked unauthorized tools")
	}
	call(readClient, "account.me", gin.H{}, 200)
	if response := management(student, "GET", "/connections", nil); bytes.Contains(response.Body.Bytes(), []byte(readToken)) || bytes.Contains(response.Body.Bytes(), []byte("token_hash")) {
		t.Fatal("connection listing leaked secret")
	}
	id, token := create(student, []string{"read", "draft", "submit"})
	client := connect(token)
	draftInput := gin.H{"category": "moral", "itemKey": "fixture_activity", "title": "CLI synthetic draft", "claim": gin.H{"score": 3}, "note": "Synthetic test"}
	args := gin.H{"idempotencyKey": "mcp-draft-0001", "input": draftInput}
	drafted := call(client, "submissions.draft", args, 201)
	draftID := fmt.Sprint(drafted["id"])
	if draftID == "<nil>" {
		t.Fatalf("missing draft id: %v", drafted)
	}
	replay := call(client, "submissions.draft", args, 201)
	if replay["id"] != drafted["id"] {
		t.Fatal("idempotent retry created another draft")
	}
	changed := gin.H{"idempotencyKey": "mcp-draft-0001", "input": gin.H{"category": "moral", "itemKey": "fixture_activity", "title": "different request", "claim": gin.H{"score": 4}}}
	call(client, "submissions.draft", changed, 409)
	read := call(client, "submissions.get", gin.H{"id": draftID}, 200)
	call(client, "submissions.update", gin.H{"id": draftID, "idempotencyKey": "mcp-update-0001", "expectedVersion": strings.Repeat("a", 64), "input": draftInput}, 409)
	call(client, "submissions.update", gin.H{"id": draftID, "idempotencyKey": "mcp-update-0002", "expectedVersion": read["resourceVersion"], "input": draftInput}, 200)
	if os.Getenv("EASYGPA_INTEGRATION") == "1" {
		objects, err := objectstore.New(objectstore.Config{Endpoint: os.Getenv("S3_ENDPOINT"), Region: os.Getenv("S3_REGION"), AccessKey: os.Getenv("S3_ACCESS_KEY"), SecretKey: os.Getenv("S3_SECRET_KEY"), Bucket: os.Getenv("S3_BUCKET")})
		if err != nil {
			t.Fatal(err)
		}
		server.deps.Objects = objects
		var file bytes.Buffer
		if err := png.Encode(&file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		ready := call(client, "evidence.prepare_upload", gin.H{"id": draftID, "idempotencyKey": "mcp-upload-0001", "input": gin.H{"filename": "synthetic-proof.png", "mediaType": "image/png", "sizeBytes": file.Len(), "sha256": mcpDigest(file.Bytes())}}, 201)
		uploadURL := fmt.Sprint(ready["uploadUrl"])
		put := func(body []byte, credential string, want int) {
			t.Helper()
			r, _ := http.NewRequest("PUT", uploadURL, bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+credential)
			r.Header.Set("Content-Type", "image/png")
			res, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			bodyResult, _ := io.ReadAll(res.Body)
			if res.StatusCode != want {
				t.Fatalf("MCP upload status=%d want=%d body=%s", res.StatusCode, want, bodyResult)
			}
		}
		corrupt := append([]byte{}, file.Bytes()...)
		corrupt[0] ^= 1
		put(corrupt, token, 422)
		_, differentConnection := create(student, []string{"read", "draft"})
		put(file.Bytes(), differentConnection, 404)
		put(file.Bytes(), token, 200)
		put(file.Bytes(), token, 200)
		call(client, "evidence.complete_upload", gin.H{"upload": ready["uploadId"], "idempotencyKey": "mcp-upload-done-0001"}, 200)
		link := call(client, "evidence.read", gin.H{"eid": ready["evidenceId"]}, 200)
		r, _ := http.NewRequest("GET", fmt.Sprint(link["url"]), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !bytes.Equal(got, file.Bytes()) {
			t.Fatalf("download mismatch status=%d", res.StatusCode)
		}
		var key string
		if err := admin.QueryRow(t.Context(), `SELECT object_key FROM evidence WHERE id=$1`, ready["evidenceId"]).Scan(&key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = objects.Remove(context.Background(), key) })
	}
	foreign := insertScoredSubmission(t, admin, other, other.StudentID, 7)
	call(client, "submissions.get", gin.H{"id": strconv.FormatInt(foreign, 10)}, 404)
	if n := countClassRows(t, admin, `SELECT count(*) FROM audit_log WHERE class_id=$1 AND action='submission.draft_created' AND metadata->>'channel'='mcp'`, fx.ClassID); n != 1 {
		t.Fatalf("MCP attribution or replay audit count=%d", n)
	}
	// A visible key cannot bypass the business lockdown middleware.
	if _, err := admin.Exec(t.Context(), `UPDATE class_timeline SET close_at=now()-interval '2 hours', lockdown_at=now()-interval '1 hour' WHERE class_id=$1`, fx.ClassID); err != nil {
		t.Fatal(err)
	}
	call(client, "submissions.draft", gin.H{"idempotencyKey": "mcp-locked-001", "input": draftInput}, 409)
	if _, err := admin.Exec(t.Context(), `UPDATE class_timeline SET close_at=now()+interval '1 day', lockdown_at=NULL WHERE class_id=$1`, fx.ClassID); err != nil {
		t.Fatal(err)
	}
	// Administrative writes cannot be executed using only the prepared ID.
	_, adminToken := create(owner, []string{"read", "manage"})
	adminClient := connect(adminToken)
	target := insertScoredSubmission(t, admin, fx, fx.StudentID, 3)
	prepArgs := gin.H{"id": strconv.FormatInt(target, 10), "idempotencyKey": "mcp-force-0001", "input": gin.H{"previousScore": 3, "score": 4, "reason": "Synthetic correction for MCP verification"}}
	prepared := call(adminClient, "class.force_score.prepare", prepArgs, 200)
	op := fmt.Sprint(prepared["operationId"])
	call(adminClient, "operations.commit", gin.H{"operationId": op}, 409)
	decision := management(owner, "POST", "/operations/"+op+"/decision", gin.H{"approve": true})
	if decision.Code != 200 {
		t.Fatalf("approve status=%d %s", decision.Code, decision.Body.String())
	}
	call(adminClient, "operations.commit", gin.H{"operationId": op}, 200)
	call(adminClient, "operations.commit", gin.H{"operationId": op}, 200)
	var score float64
	if err := admin.QueryRow(t.Context(), `SELECT final_score::float8 FROM submission WHERE id=$1`, target).Scan(&score); err != nil || score != 4 {
		t.Fatalf("approved write not applied: %v %v", score, err)
	}
	prepArgs["idempotencyKey"] = "mcp-force-0002"
	prepArgs["input"] = gin.H{"previousScore": 4, "score": 5, "reason": "Another synthetic correction"}
	prepared = call(adminClient, "class.force_score.prepare", prepArgs, 200)
	op = fmt.Sprint(prepared["operationId"])
	if response := management(student, "POST", "/operations/"+op+"/decision", gin.H{"approve": true}); response.Code != 404 {
		t.Fatal("another account approved operation")
	}
	if response := management(owner, "POST", "/operations/"+op+"/decision", gin.H{"approve": true}); response.Code != 200 {
		t.Fatal("approval failed")
	}
	if _, err := admin.Exec(t.Context(), `UPDATE submission SET title=title||' changed' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	call(adminClient, "operations.commit", gin.H{"operationId": op}, 409)
	// Token scoping and revocation are checked again on every HTTP request.
	response := management(student, "DELETE", "/connections/"+id, nil)
	if response.Code != 204 {
		t.Fatal("revoke failed")
	}
	rawRequest := func(rawToken, origin string) int {
		t.Helper()
		request, _ := http.NewRequest("POST", httpServer.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		request.Header.Set("Authorization", "Bearer "+rawToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	if rawRequest(token, "") != 401 || rawRequest(readToken, "https://untrusted.example.org") != 403 || rawRequest("browser-session-token", "") != 401 {
		t.Fatal("revocation, origin or token audience check missing")
	}
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET status='disabled' WHERE id=$1`, fx.StudentID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET status='active' WHERE id=$1`, fx.StudentID); err != nil {
		t.Fatal(err)
	}
	if rawRequest(readToken, "") != 401 {
		t.Fatal("re-enabled account restored revoked credentials")
	}
	_, newToken := create(student, []string{"read"})
	if rawRequest(newToken, "") != 200 {
		t.Fatal("new credential unavailable")
	}
	if _, err := admin.Exec(t.Context(), `UPDATE agent_connection SET expires_at=now()-interval '1 minute' WHERE token_hash=$1`, sha256Sum(newToken)); err != nil {
		t.Fatal(err)
	}
	if rawRequest(newToken, "") != 401 {
		t.Fatal("expired credential accepted")
	}
	_, passwordToken := create(student, []string{"read"})
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET password_hash=NULL WHERE id=$1`, student.UserID); err != nil {
		t.Fatal(err)
	}
	if rawRequest(passwordToken, "") != 401 {
		t.Fatal("password reset retained connection")
	}
	if _, err := admin.Exec(t.Context(), `UPDATE app_user SET password_hash=$1 WHERE id=$2`, hash, student.UserID); err != nil {
		t.Fatal(err)
	}
	if rawRequest(passwordToken, "") != 401 {
		t.Fatal("password reset connection revived")
	}
	_, archiveToken := create(student, []string{"read"})
	if _, err := admin.Exec(t.Context(), `UPDATE class SET archived=true WHERE id=$1`, fx.ClassID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(t.Context(), `UPDATE class SET archived=false WHERE id=$1`, fx.ClassID); err != nil {
		t.Fatal(err)
	}
	if rawRequest(archiveToken, "") != 401 {
		t.Fatal("class reactivation restored old connection")
	}
}

func sha256Sum(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }

func TestMCPRevokedExportNeverRenders(t *testing.T) {
	admin := settlementAdminPool(t)
	app := settlementAppPool(t, admin.Config().ConnString())
	fx := seedSettlementClass(t, admin, true)
	run := persistForcedSettlement(t, app, fx)
	_, digest, err := mcpSecret()
	if err != nil {
		t.Fatal(err)
	}
	var connectionID int64
	if err := admin.QueryRow(t.Context(), `INSERT INTO agent_connection(class_id,user_id,name,token_prefix,token_hash,scopes,auth_version,expires_at,revoked_at) VALUES($1,$2,'Synthetic export','test',$3,ARRAY['export'],1,now()+interval '1 hour',now()) RETURNING id`, fx.ClassID, fx.AdminID, digest).Scan(&connectionID); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := admin.QueryRow(t.Context(), `INSERT INTO export_job(class_id,run_id,kind,status,requested_by,agent_connection_id) VALUES($1,$2,'summary','queued',$3,$4) RETURNING id::text`, fx.ClassID, run.RunID, fx.AdminID, connectionID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	// An unconfigured object client would fail/panic if rendering was reached.
	worker, err := exportjob.NewWorker(app, &objectstore.Client{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.ExportRequestedPayload{JobID: jobID, RunID: run.RunID, Kind: "summary"})
	if err := worker.Handle(t.Context(), events.Event{ClassID: fx.ClassID, Type: events.ExportRequested, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var status string
	var key *string
	if err := admin.QueryRow(t.Context(), `SELECT status,object_key FROM export_job WHERE id=$1::uuid`, jobID).Scan(&status, &key); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || key != nil {
		t.Fatalf("revoked export was not cancelled: %s", status)
	}
}
