package uxtesting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testTarget() Target {
	return Target{
		Kind:    TargetPullRequest,
		Repo:    "msgmate-io/open-chat-go",
		Number:  1051,
		Ref:     "feat/ux-testing-agent",
		Feature: "open the bot settings page and switch the model",
	}
}

func TestDefaultProfileValidates(t *testing.T) {
	profile := DefaultProfile()
	if err := profile.Validate(); err != nil {
		t.Fatalf("default profile must validate: %v", err)
	}
	if !profile.Browser.RecordVideo {
		t.Fatal("default profile must record the playwright session")
	}
	if !profile.Publish.RequireConfirmation {
		t.Fatal("default profile must wait for the human confirmation before publishing")
	}
}

func TestProfileValidateRejectsIncompleteConfigs(t *testing.T) {
	cases := map[string]func(p *Profile){
		"sandbox image":     func(p *Profile) { p.Sandbox.Image = " " },
		"sandbox workspace": func(p *Profile) { p.Sandbox.Workspace = "" },
		"sandbox network":   func(p *Profile) { p.Sandbox.AllowNetwork = false },
		"mcp name":          func(p *Profile) { p.Browser.MCPName = "" },
		"browser":           func(p *Profile) { p.Browser.Browser = "" },
		"viewport":          func(p *Profile) { p.Browser.Viewport = "1280*720" },
		"recording off":     func(p *Profile) { p.Browser.RecordVideo = false },
		"video size":        func(p *Profile) { p.Browser.VideoSize = "" },
		"start command":     func(p *Profile) { p.Server.StartCommand = nil },
		"health url":        func(p *Profile) { p.Server.HealthURL = "" },
		"ready timeout":     func(p *Profile) { p.Server.ReadyTimeout = 0 },
		"recording dir":     func(p *Profile) { p.Recording.Dir = "" },
		"recording format":  func(p *Profile) { p.Recording.Format = "gif" },
		"fps":               func(p *Profile) { p.Recording.FramesPerSecond = 0 },
		"max duration":      func(p *Profile) { p.Recording.MaxDuration = 0 },
		"attachment name":   func(p *Profile) { p.Publish.AttachmentName = "" },
	}
	for name, mutate := range cases {
		profile := DefaultProfile()
		mutate(&profile)
		if err := profile.Validate(); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
}

func TestProfileUsingExistingServerSkipsStartCommand(t *testing.T) {
	profile := DefaultProfile()
	profile.Server.BaseURL = "http://127.0.0.1:8080"
	profile.Server.StartCommand = nil
	profile.Server.HealthURL = ""
	if err := profile.Validate(); err != nil {
		t.Fatalf("an already running server must not require a start command: %v", err)
	}
	plan, err := profile.PlanFor(testTarget())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !strings.Contains(plan.Phases[2].Detail, "http://127.0.0.1:8080") {
		t.Fatalf("wait phase must target the given base url, got %q", plan.Phases[2].Detail)
	}
}

func TestTargetValidateAndRender(t *testing.T) {
	target := testTarget()
	if err := target.Validate(); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	if got, want := target.String(), "msgmate-io/open-chat-go#1051"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if got, want := target.ThreadURL(), "https://github.com/msgmate-io/open-chat-go/pull/1051"; got != want {
		t.Fatalf("ThreadURL() = %q, want %q", got, want)
	}

	issue := testTarget()
	issue.Kind = TargetIssue
	if got, want := issue.ThreadURL(), "https://github.com/msgmate-io/open-chat-go/issues/1051"; got != want {
		t.Fatalf("issue ThreadURL() = %q, want %q", got, want)
	}

	for name, mutate := range map[string]func(t *Target){
		"repo":   func(t *Target) { t.Repo = "" },
		"owner":  func(t *Target) { t.Repo = "open-chat-go" },
		"number": func(t *Target) { t.Number = 0 },
		"kind":   func(t *Target) { t.Kind = "commit" },
	} {
		bad := testTarget()
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("%s: expected a validation error", name)
		}
	}
}

func TestPlanForOrdersEveryPhase(t *testing.T) {
	plan, err := DefaultProfile().PlanFor(testTarget())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan must validate: %v", err)
	}
	kinds := plan.Kinds()
	if len(kinds) != len(PhaseAll) {
		t.Fatalf("got %d phases, want %d", len(kinds), len(PhaseAll))
	}
	for idx, kind := range kinds {
		if kind != PhaseAll[idx] {
			t.Fatalf("phase %d = %q, want %q", idx, kind, PhaseAll[idx])
		}
	}
	if plan.Profile != "default" {
		t.Fatalf("profile = %q, want default", plan.Profile)
	}
}

func TestPlanValidateRejectsMisorderedPhases(t *testing.T) {
	plan, err := DefaultProfile().PlanFor(testTarget())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	plan.Phases[0], plan.Phases[5] = plan.Phases[5], plan.Phases[0]
	if err := plan.Validate(); err == nil {
		t.Fatal("expected a misordered plan to be rejected")
	}
}

func TestSessionCommentEmbedsStandaloneVideo(t *testing.T) {
	plan, err := DefaultProfile().PlanFor(testTarget())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	session, err := NewSession(plan, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	for _, kind := range PhaseAll[:5] {
		session.Complete(kind)
	}
	session.Notes = []string{"the model dropdown was empty before reloading"}
	if err := session.Finish(
		OutcomeFailed,
		Recording{Path: "test-results/session.webm", Duration: 90 * time.Second, Bytes: 2048},
		Attachment{
			Name:     "ux-testing-session.webm",
			URL:      "https://cdn.example.test/ux-testing-session.webm",
			MimeType: "video/webm",
			Bytes:    2048,
		},
		time.Unix(120, 0),
	); err != nil {
		t.Fatalf("finish: %v", err)
	}

	comment, err := session.Comment()
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	for _, want := range []string{
		"UX testing session",
		"msgmate-io/open-chat-go#1051",
		"**failed**",
		"`feat/ux-testing-agent`",
		`<video src="https://cdn.example.test/ux-testing-session.webm" controls width="100%"></video>`,
		"Download the recording (ux-testing-session.webm)",
		"open the bot settings page and switch the model",
		"the model dropdown was empty before reloading",
		"2.0 KiB",
	} {
		if !strings.Contains(comment, want) {
			t.Fatalf("comment is missing %q:\n%s", want, comment)
		}
	}
}

func TestSessionCommentRequiresVideo(t *testing.T) {
	plan, err := DefaultProfile().PlanFor(testTarget())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	session, err := NewSession(plan, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	session.Outcome = OutcomePassed
	if _, err := session.Comment(); err == nil {
		t.Fatal("a comment without an uploaded video must be rejected")
	}
}

func TestSessionFinishRejectsBadArtifacts(t *testing.T) {
	plan, err := DefaultProfile().PlanFor(testTarget())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	session, err := NewSession(plan, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	good := Recording{Path: "test-results/s.webm", Duration: time.Second, Bytes: 1}
	attachment := Attachment{Name: "s.webm", URL: "https://cdn.test/s.webm", MimeType: "video/webm", Bytes: 1}
	if err := session.Finish("maybe", good, attachment, time.Unix(1, 0)); err == nil {
		t.Fatal("expected an unknown outcome to be rejected")
	}
	if err := session.Finish(OutcomePassed, Recording{}, attachment, time.Unix(1, 0)); err == nil {
		t.Fatal("expected an empty recording to be rejected")
	}
	notVideo := attachment
	notVideo.MimeType = "image/png"
	if err := session.Finish(OutcomePassed, good, notVideo, time.Unix(1, 0)); err == nil {
		t.Fatal("expected a non-video attachment to be rejected")
	}
}

// --- fakes -------------------------------------------------------------

type fakeSandbox struct {
	checks       int
	failChecks   int
	recordings   map[string][]byte
	commands     [][]string
	checkedOut   bool
	teardowns    int
	checkoutErr  error
	startCommand error
}

func (f *fakeSandbox) Checkout(ctx context.Context, target Target, spec SandboxSpec) error {
	if f.checkoutErr != nil {
		return f.checkoutErr
	}
	f.checkedOut = true
	return nil
}

func (f *fakeSandbox) Run(ctx context.Context, command []string) (string, error) {
	f.commands = append(f.commands, command)
	if command[0] == "curl" {
		f.checks++
		if f.checks > f.failChecks {
			return "", nil
		}
		return "", errors.New("connection refused")
	}
	if f.startCommand != nil {
		return "", f.startCommand
	}
	return "", nil
}

func (f *fakeSandbox) ReadFile(ctx context.Context, path string) ([]byte, error) {
	data, ok := f.recordings[path]
	if !ok {
		return nil, errors.New("no such file")
	}
	return data, nil
}

func (f *fakeSandbox) Teardown(ctx context.Context) error {
	f.teardowns++
	return nil
}

type fakeBrowser struct {
	driveErr     error
	notes        []string
	recording    Recording
	closeErr     error
	navigated    string
	closed       bool
	openErr      error
	navigateErr  error
	scenarioRead string
}

func (f *fakeBrowser) Open(ctx context.Context, spec BrowserSpec, dir string) error { return f.openErr }

func (f *fakeBrowser) Navigate(ctx context.Context, url string) error {
	if f.navigateErr != nil {
		return f.navigateErr
	}
	f.navigated = url
	return nil
}

func (f *fakeBrowser) Drive(ctx context.Context, scenario string) ([]string, error) {
	f.scenarioRead = scenario
	return f.notes, f.driveErr
}

func (f *fakeBrowser) Close(ctx context.Context) (Recording, error) {
	f.closed = true
	return f.recording, f.closeErr
}

type fakePublisher struct {
	uploadedName string
	uploadedMime string
	uploadedData []byte
	attachment   Attachment
	uploadErr    error
	commentErr   error
	comments     []string
	commentTo    []Target
}

func (f *fakePublisher) Upload(ctx context.Context, name, mimeType string, data []byte) (Attachment, error) {
	if f.uploadErr != nil {
		return Attachment{}, f.uploadErr
	}
	f.uploadedName = name
	f.uploadedMime = mimeType
	f.uploadedData = data
	return f.attachment, nil
}

func (f *fakePublisher) Comment(ctx context.Context, target Target, body string) error {
	if f.commentErr != nil {
		return f.commentErr
	}
	f.comments = append(f.comments, body)
	f.commentTo = append(f.commentTo, target)
	return nil
}

func happyRunner() (Runner, *fakeSandbox, *fakeBrowser, *fakePublisher) {
	sandbox := &fakeSandbox{
		failChecks: 1,
		recordings: map[string][]byte{"test-results/session.webm": []byte("webm-bytes")},
	}
	browser := &fakeBrowser{
		notes:     []string{"clicked the model dropdown"},
		recording: Recording{Path: "test-results/session.webm", Duration: 42 * time.Second},
	}
	publisher := &fakePublisher{attachment: Attachment{
		Name:     "ux-testing-session.webm",
		URL:      "https://cdn.test/ux-testing-session.webm",
		MimeType: "video/webm",
		Bytes:    10,
	}}
	return Runner{
		Sandbox:      sandbox,
		Browser:      browser,
		Publisher:    publisher,
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return time.Unix(100, 0) },
	}, sandbox, browser, publisher
}

// --- runner ------------------------------------------------------------

func TestRunnerPublishesRecording(t *testing.T) {
	runner, sandbox, browser, publisher := happyRunner()

	session, err := runner.Run(context.Background(), DefaultProfile(), testTarget())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if session.Outcome != OutcomePassed {
		t.Fatalf("outcome = %q, want passed", session.Outcome)
	}
	if !sandbox.checkedOut || sandbox.teardowns != 1 {
		t.Fatalf("sandbox not checked out or torn down: %+v", sandbox)
	}
	if browser.navigated != "http://127.0.0.1:3000/" {
		t.Fatalf("browser navigated to %q", browser.navigated)
	}
	if !browser.closed {
		t.Fatal("browser session must be closed to flush the recording")
	}
	if browser.scenarioRead != testTarget().Feature {
		t.Fatalf("scenario = %q", browser.scenarioRead)
	}
	if got := len(session.Completed); got != len(PhaseAll) {
		t.Fatalf("completed %d phases, want %d", got, len(PhaseAll))
	}
	if publisher.uploadedName != "ux-testing-session.webm" || publisher.uploadedMime != "video/webm" {
		t.Fatalf("uploaded %q as %q", publisher.uploadedName, publisher.uploadedMime)
	}
	if string(publisher.uploadedData) != "webm-bytes" {
		t.Fatalf("uploaded data = %q", string(publisher.uploadedData))
	}
	if len(publisher.comments) != 1 {
		t.Fatalf("posted %d comments, want 1", len(publisher.comments))
	}
	if !strings.Contains(publisher.comments[0], "<video src=") {
		t.Fatal("comment must embed the recording as a standalone video")
	}
	if publisher.commentTo[0] != testTarget() {
		t.Fatalf("comment posted to %+v", publisher.commentTo[0])
	}
}

func TestRunnerPublishesFailedSessionRecording(t *testing.T) {
	runner, _, browser, publisher := happyRunner()
	browser.driveErr = errors.New("the model dropdown never loaded")

	session, err := runner.Run(context.Background(), DefaultProfile(), testTarget())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if session.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", session.Outcome)
	}
	if len(publisher.comments) != 1 {
		t.Fatal("a failed run must still publish its recording")
	}
	if !strings.Contains(publisher.comments[0], "the model dropdown never loaded") {
		t.Fatal("comment must carry the observation")
	}
}

func TestRunnerRequiresDependencies(t *testing.T) {
	_, err := Runner{}.Run(context.Background(), DefaultProfile(), testTarget())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestRunnerRejectsInvalidTarget(t *testing.T) {
	runner, _, _, _ := happyRunner()
	bad := testTarget()
	bad.Number = 0
	if _, err := runner.Run(context.Background(), DefaultProfile(), bad); err == nil {
		t.Fatal("expected an invalid target to be rejected")
	}
}

func TestRunnerErrorsWhenRecordingIsMissing(t *testing.T) {
	runner, _, browser, _ := happyRunner()
	browser.recording = Recording{}

	_, err := runner.Run(context.Background(), DefaultProfile(), testTarget())
	if !errors.Is(err, ErrRecordingMissing) {
		t.Fatalf("err = %v, want ErrRecordingMissing", err)
	}
}

func TestRunnerErrorsWhenRecordingFileIsMissing(t *testing.T) {
	runner, sandbox, _, _ := happyRunner()
	sandbox.recordings = map[string][]byte{}

	_, err := runner.Run(context.Background(), DefaultProfile(), testTarget())
	if !errors.Is(err, ErrRecordingMissing) {
		t.Fatalf("err = %v, want ErrRecordingMissing", err)
	}
}

func TestRunnerFailsWhenDevServerNeverAnswers(t *testing.T) {
	runner, sandbox, _, _ := happyRunner()
	sandbox.failChecks = 1 << 20
	profile := DefaultProfile()
	profile.Server.ReadyTimeout = 5 * time.Millisecond

	_, err := runner.Run(context.Background(), profile, testTarget())
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v, want a dev-server timeout", err)
	}
	if sandbox.teardowns != 1 {
		t.Fatal("the sandbox must be torn down even when the server never answers")
	}
}

func TestRunnerPropagatesCheckoutFailure(t *testing.T) {
	runner, sandbox, _, _ := happyRunner()
	sandbox.checkoutErr = errors.New("clone failed")

	_, err := runner.Run(context.Background(), DefaultProfile(), testTarget())
	if err == nil || !strings.Contains(err.Error(), "prepare workspace") {
		t.Fatalf("err = %v, want a prepare-workspace error", err)
	}
}

func TestBotConfigCarriesPlaywrightSandboxProfile(t *testing.T) {
	config := DefaultProfile().BotConfig()
	if config["system_prompt"] != SystemPrompt {
		t.Fatal("bot config must carry the ux testing prompt")
	}
	tools, ok := config["tools"].([]interface{})
	if !ok || len(tools) != len(PlaywrightTools) {
		t.Fatalf("bot config tools = %v", config["tools"])
	}
	section, ok := config[ConfigKey].(map[string]interface{})
	if !ok {
		t.Fatalf("bot config must carry the %q section", ConfigKey)
	}
	for _, key := range []string{
		"sandbox_image", "sandbox_workspace", "playwright_mcp", "browser",
		"viewport", "record_video", "recording_dir", "dev_server_health",
		"publish_attachment", "require_confirmation",
	} {
		if _, ok := section[key]; !ok {
			t.Fatalf("bot config section is missing %q", key)
		}
	}
	if section["record_video"] != true {
		t.Fatal("bot config must enable playwright recording")
	}
}

func TestMimeTypeForFormat(t *testing.T) {
	if got := mimeTypeForFormat("webm"); got != "video/webm" {
		t.Fatalf("webm = %q", got)
	}
	if got := mimeTypeForFormat("MP4"); got != "video/mp4" {
		t.Fatalf("mp4 = %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	if got := humanBytes(512); got != "512 B" {
		t.Fatalf("512 = %q", got)
	}
	if got := humanBytes(2048); got != "2.0 KiB" {
		t.Fatalf("2048 = %q", got)
	}
}
