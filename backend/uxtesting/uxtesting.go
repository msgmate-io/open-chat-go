// Package uxtesting provides the general base abstraction for the UX testing
// agent (ci#1051).
//
// The agent is configured like any other coding agent - an isolated sandbox
// with ordinary development repository access plus the Playwright MCP server -
// but its job is not to change code. It takes a feature or a change (usually a
// pull request), brings the development server up inside its sandbox (through
// the docker tools or directly), drives the running UI through the Playwright
// MCP while Playwright records the session, and posts that recording as a
// standalone video into the pull-request or issue thread where the agent was
// mentioned.
//
// This package deliberately performs no I/O. It defines
//
//   - the Profile an operator/integration configures (sandbox, browser MCP,
//     development server, recording and publishing), with a DefaultProfile that
//     works for every repository currently wired to ci.msgmate.io;
//   - the Target (the pull request/issue plus the feature description the
//     agent was asked to test);
//   - the ordered Plan a run follows;
//   - the Recording/Attachment artifact and the Session record of a run;
//   - the thread comment that embeds the recording as a standalone video;
//   - the bot bootstrap config an integration applies to register the UX
//     testing agent as one of its default agents.
//
// The private integrations that own the sandbox, the repository and the
// GitHub credentials only supply the mechanics, so a single implementation
// serves every configured repository instead of one bespoke bot per repo.
package uxtesting

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// AgentName is the runtime name of the default UX testing agent bot.
const AgentName = "ux-testing-bot"

// ConfigKey is the shared-config key that carries a UX testing agent profile.
const ConfigKey = "ux_testing"

// DefaultPlaywrightMCPName is the MCP server name the profile expects to be
// attached to the interaction (the Playwright MCP server).
const DefaultPlaywrightMCPName = "playwright"

// PlaywrightTools are the Playwright MCP tools a UX testing agent needs: a
// browser to drive, snapshots to decide what to do next, and the built-in
// recording that Playwright writes into RecordingSpec.Dir.
var PlaywrightTools = []string{
	"browser_navigate",
	"browser_snapshot",
	"browser_click",
	"browser_type",
	"browser_press_key",
	"browser_wait_for",
	"browser_take_screenshot",
	"browser_evaluate",
	"browser_console_messages",
	"browser_network_requests",
	"browser_close",
}

// TargetKind distinguishes the thread the agent was mentioned in.
type TargetKind string

const (
	// TargetPullRequest is a pull request thread.
	TargetPullRequest TargetKind = "pull_request"
	// TargetIssue is an issue thread.
	TargetIssue TargetKind = "issue"
)

// Target is the feature or change the agent was asked to test.
type Target struct {
	// Kind is the thread kind (pull_request or issue).
	Kind TargetKind
	// Repo is the repository in owner/name form.
	Repo string
	// Number is the pull request or issue number.
	Number int
	// Ref is the optional git ref under test (the PR head branch/sha).
	Ref string
	// Feature is the free-form description of what should be exercised.
	Feature string
}

// Validate reports whether the target can be tested.
func (t Target) Validate() error {
	if strings.TrimSpace(t.Repo) == "" {
		return errors.New("uxtesting: target repo is required")
	}
	if parts := strings.Split(t.Repo, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("uxtesting: target repo %q must be owner/name", t.Repo)
	}
	if t.Number <= 0 {
		return fmt.Errorf("uxtesting: target number must be positive, got %d", t.Number)
	}
	switch t.Kind {
	case TargetPullRequest, TargetIssue:
	default:
		return fmt.Errorf("uxtesting: unsupported target kind %q", t.Kind)
	}
	return nil
}

// String renders the target as owner/name#number.
func (t Target) String() string {
	return fmt.Sprintf("%s#%d", t.Repo, t.Number)
}

// ThreadURL is the canonical public URL of the thread the agent reports into.
func (t Target) ThreadURL() string {
	segment := "issues"
	if t.Kind == TargetPullRequest {
		segment = "pull"
	}
	return fmt.Sprintf("https://github.com/%s/%s/%d", t.Repo, segment, t.Number)
}

// SandboxSpec describes the isolated development sandbox the agent runs in.
type SandboxSpec struct {
	// Image is the sandbox image (it ships the repository toolchain plus the
	// browser Playwright drives).
	Image string
	// Workspace is where the repository is checkout out inside the sandbox.
	Workspace string
	// CPULimit/MemoryLimit bound the development server and the browser.
	CPULimit    string
	MemoryLimit string
	// AllowNetwork must stay true: the browser reaches the dev server and the
	// agent reaches the repository remote.
	AllowNetwork bool
}

// BrowserSpec describes the Playwright MCP the agent drives.
type BrowserSpec struct {
	// MCPName is the MCP server name carrying the Playwright tools.
	MCPName string
	// MCPURL optionally pins a shared/remote Playwright MCP endpoint; empty
	// means the sandbox-local server.
	MCPURL string
	// Browser is the Playwright browser to launch (chromium by default).
	Browser string
	// Viewport is the browser viewport, e.g. 1280x720.
	Viewport string
	// Headless keeps the run reproducible; recording works headless.
	Headless bool
	// RecordVideo enables Playwright's built-in session recording.
	RecordVideo bool
	// VideoSize is the recorded video size, e.g. 1280x720.
	VideoSize string
}

// ServerSpec describes the development server the agent brings up.
type ServerSpec struct {
	// BaseURL is an already-running server to test instead of starting one
	// (empty means "start it in the sandbox").
	BaseURL string
	// StartCommand starts the development server in the sandbox.
	StartCommand []string
	// StopCommand optionally tears it down again.
	StopCommand []string
	// HealthURL is polled until the server answers 2xx/3xx.
	HealthURL string
	// ReadyTimeout bounds the start-up wait.
	ReadyTimeout time.Duration
}

// RecordingSpec describes where and how the session is recorded.
type RecordingSpec struct {
	// Dir is the sandbox directory Playwright writes recordings into.
	Dir string
	// Format is the video container (webm or mp4).
	Format string
	// FramesPerSecond is the capture rate.
	FramesPerSecond int
	// MaxDuration caps a single recorded run.
	MaxDuration time.Duration
}

// PublishSpec describes how the recording reaches the thread.
type PublishSpec struct {
	// AttachmentName is the file name the recording is uploaded as.
	AttachmentName string
	// CommentPrefix heads the thread comment.
	CommentPrefix string
	// RequireConfirmation makes publishing wait for the human who selected
	// the agent, so a bot-mention never posts on its own.
	RequireConfirmation bool
}

// Profile is a complete UX testing agent configuration.
type Profile struct {
	Name      string
	Sandbox   SandboxSpec
	Browser   BrowserSpec
	Server    ServerSpec
	Recording RecordingSpec
	Publish   PublishSpec
}

// DefaultProfile is the general profile every repository configured for
// ci.msgmate.io can run without per-repository tuning: bring the repository up
// in the standard UX testing sandbox, drive it with the sandbox-local
// Playwright MCP and record the session.
func DefaultProfile() Profile {
	return Profile{
		Name: "default",
		Sandbox: SandboxSpec{
			Image:        "ghcr.io/msgmate-io/open-chat-ux-testing-sandbox:latest",
			Workspace:    "/workspace/repo",
			CPULimit:     "4",
			MemoryLimit:  "8g",
			AllowNetwork: true,
		},
		Browser: BrowserSpec{
			MCPName:     DefaultPlaywrightMCPName,
			Browser:     "chromium",
			Viewport:    "1280x720",
			Headless:    true,
			RecordVideo: true,
			VideoSize:   "1280x720",
		},
		Server: ServerSpec{
			StartCommand: []string{"bash", "-lc", "make dev-server"},
			HealthURL:    "http://127.0.0.1:3000/",
			ReadyTimeout: 5 * time.Minute,
		},
		Recording: RecordingSpec{
			Dir:             "test-results",
			Format:          "webm",
			FramesPerSecond: 25,
			MaxDuration:     10 * time.Minute,
		},
		Publish: PublishSpec{
			AttachmentName:      "ux-testing-session.webm",
			CommentPrefix:       "UX testing session",
			RequireConfirmation: true,
		},
	}
}

// Validate reports whether the profile is complete enough to run.
func (p Profile) Validate() error {
	if strings.TrimSpace(p.Sandbox.Image) == "" {
		return errors.New("uxtesting: sandbox image is required")
	}
	if strings.TrimSpace(p.Sandbox.Workspace) == "" {
		return errors.New("uxtesting: sandbox workspace is required")
	}
	if !p.Sandbox.AllowNetwork {
		return errors.New("uxtesting: sandbox network access is required to start a dev server and reach the browser")
	}
	if strings.TrimSpace(p.Browser.MCPName) == "" {
		return errors.New("uxtesting: playwright mcp server name is required")
	}
	if strings.TrimSpace(p.Browser.Browser) == "" {
		return errors.New("uxtesting: browser is required")
	}
	if !validSizes(p.Browser.Viewport) {
		return fmt.Errorf("uxtesting: browser viewport %q must be <width>x<height>", p.Browser.Viewport)
	}
	if !p.Browser.RecordVideo {
		return errors.New("uxtesting: browser recording must be enabled to publish a session video")
	}
	if !validSizes(p.Browser.VideoSize) {
		return fmt.Errorf("uxtesting: video size %q must be <width>x<height>", p.Browser.VideoSize)
	}
	if strings.TrimSpace(p.Server.BaseURL) == "" {
		if len(p.Server.StartCommand) == 0 {
			return errors.New("uxtesting: dev server start command (or an existing base URL) is required")
		}
		if strings.TrimSpace(p.Server.HealthURL) == "" {
			return errors.New("uxtesting: dev server health URL is required")
		}
		if p.Server.ReadyTimeout <= 0 {
			return errors.New("uxtesting: dev server ready timeout must be positive")
		}
	}
	if strings.TrimSpace(p.Recording.Dir) == "" {
		return errors.New("uxtesting: recording directory is required")
	}
	switch strings.ToLower(strings.TrimSpace(p.Recording.Format)) {
	case "webm", "mp4":
	default:
		return fmt.Errorf("uxtesting: recording format %q must be webm or mp4", p.Recording.Format)
	}
	if p.Recording.FramesPerSecond <= 0 {
		return errors.New("uxtesting: recording frames per second must be positive")
	}
	if p.Recording.MaxDuration <= 0 {
		return errors.New("uxtesting: recording max duration must be positive")
	}
	if strings.TrimSpace(p.Publish.AttachmentName) == "" {
		return errors.New("uxtesting: publish attachment name is required")
	}
	return nil
}

func validSizes(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "x")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// PhaseKind names one step of a UX testing run.
type PhaseKind string

const (
	// PhasePrepareWorkspace checks the repository out at the target ref and
	// installs its dependencies inside the sandbox.
	PhasePrepareWorkspace PhaseKind = "prepare_workspace"
	// PhaseStartDevServer brings the development server up.
	PhaseStartDevServer PhaseKind = "start_dev_server"
	// PhaseWaitForServer polls the health URL until the UI is reachable.
	PhaseWaitForServer PhaseKind = "wait_for_server"
	// PhaseDriveUI drives the feature through the Playwright MCP while
	// Playwright records.
	PhaseDriveUI PhaseKind = "drive_playwright_scenario"
	// PhaseCollectRecording validates and reads the recorded video.
	PhaseCollectRecording PhaseKind = "collect_recording"
	// PhasePublishRecording posts the standalone video into the thread.
	PhasePublishRecording PhaseKind = "publish_recording"
)

// PhaseAll lists the phases of a run in execution order.
var PhaseAll = []PhaseKind{
	PhasePrepareWorkspace,
	PhaseStartDevServer,
	PhaseWaitForServer,
	PhaseDriveUI,
	PhaseCollectRecording,
	PhasePublishRecording,
}

// Phase is one ordered step of a run.
type Phase struct {
	Kind   PhaseKind
	Title  string
	Detail string
	Budget time.Duration
}

// Plan is the ordered set of phases for one target.
type Plan struct {
	Target  Target
	Profile string
	Phases  []Phase
}

// PlanFor builds the run plan for a target. It validates the profile and the
// target so a misconfigured agent fails before a sandbox is ever started.
func (p Profile) PlanFor(t Target) (Plan, error) {
	if err := p.Validate(); err != nil {
		return Plan{}, err
	}
	if err := t.Validate(); err != nil {
		return Plan{}, err
	}
	server := p.Server
	if strings.TrimSpace(server.BaseURL) != "" {
		server.HealthURL = server.BaseURL
	}
	feature := strings.TrimSpace(t.Feature)
	if feature == "" {
		feature = "the change under test"
	}
	phases := []Phase{
		{
			Kind:   PhasePrepareWorkspace,
			Title:  "Prepare workspace",
			Detail: fmt.Sprintf("check out %s (%s) into %s and install dependencies", t.String(), refOrDefault(t.Ref), p.Sandbox.Workspace),
		},
		{
			Kind:   PhaseStartDevServer,
			Title:  "Start development server",
			Detail: fmt.Sprintf("run %s in %s", strings.Join(p.Server.StartCommand, " "), p.Sandbox.Workspace),
		},
		{
			Kind:   PhaseWaitForServer,
			Title:  "Wait for the UI",
			Detail: fmt.Sprintf("poll %s until it answers", server.HealthURL),
			Budget: p.Server.ReadyTimeout,
		},
		{
			Kind:   PhaseDriveUI,
			Title:  "Drive the UI with the Playwright MCP",
			Detail: fmt.Sprintf("exercise %s against %s and record the session with %s", feature, server.HealthURL, p.Browser.MCPName),
			Budget: p.Recording.MaxDuration,
		},
		{
			Kind:   PhaseCollectRecording,
			Title:  "Collect the recording",
			Detail: fmt.Sprintf("read the %s video Playwright wrote into %s", strings.ToLower(p.Recording.Format), p.Recording.Dir),
		},
		{
			Kind:   PhasePublishRecording,
			Title:  "Publish the recording",
			Detail: fmt.Sprintf("attach %s to %s", p.Publish.AttachmentName, t.ThreadURL()),
		},
	}
	return Plan{Target: t, Profile: p.Name, Phases: phases}, nil
}

func refOrDefault(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "head"
	}
	return ref
}

// Kinds returns the phase kinds in plan order.
func (p Plan) Kinds() []PhaseKind {
	kinds := make([]PhaseKind, 0, len(p.Phases))
	for _, phase := range p.Phases {
		kinds = append(kinds, phase.Kind)
	}
	return kinds
}

// Validate reports whether the plan is complete and correctly ordered: every
// phase present exactly once, publishing last.
func (p Plan) Validate() error {
	if err := p.Target.Validate(); err != nil {
		return err
	}
	if len(p.Phases) != len(PhaseAll) {
		return fmt.Errorf("uxtesting: plan has %d phases, want %d", len(p.Phases), len(PhaseAll))
	}
	seen := map[PhaseKind]bool{}
	for idx, phase := range p.Phases {
		if phase.Kind != PhaseAll[idx] {
			return fmt.Errorf("uxtesting: phase %d is %q, want %q", idx, phase.Kind, PhaseAll[idx])
		}
		if seen[phase.Kind] {
			return fmt.Errorf("uxtesting: duplicate phase %q", phase.Kind)
		}
		seen[phase.Kind] = true
	}
	return nil
}

// Recording is the video artifact Playwright produced for a run.
type Recording struct {
	// Path is the sandbox-local path of the recorded video.
	Path string
	// Duration is how long the session ran.
	Duration time.Duration
	// Bytes is the recording size.
	Bytes int64
}

// Validate reports whether the recording looks publishable.
func (r Recording) Validate() error {
	if strings.TrimSpace(r.Path) == "" {
		return errors.New("uxtesting: recording path is required")
	}
	if r.Duration <= 0 {
		return errors.New("uxtesting: recording duration must be positive")
	}
	if r.Bytes <= 0 {
		return errors.New("uxtesting: recording size must be positive")
	}
	return nil
}

// Attachment is the uploaded recording as referenced from the thread comment.
type Attachment struct {
	// Name is the uploaded file name.
	Name string
	// URL is the upload URL the thread can embed.
	URL string
	// MimeType is the video mime type (video/webm or video/mp4).
	MimeType string
	// Bytes is the uploaded size.
	Bytes int64
}

// Validate reports whether the attachment can be embedded.
func (a Attachment) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("uxtesting: attachment name is required")
	}
	if strings.TrimSpace(a.URL) == "" {
		return errors.New("uxtesting: attachment url is required")
	}
	if !strings.HasPrefix(strings.TrimSpace(a.MimeType), "video/") {
		return fmt.Errorf("uxtesting: attachment %q must be a video, got %q", a.Name, a.MimeType)
	}
	if a.Bytes <= 0 {
		return errors.New("uxtesting: attachment size must be positive")
	}
	return nil
}

// Outcome is the verdict of a UX testing run.
type Outcome string

const (
	// OutcomePassed means the feature could be exercised as asked.
	OutcomePassed Outcome = "passed"
	// OutcomeFailed means the run could not exercise the feature (a product
	// bug or a broken development server); the recording is still published.
	OutcomeFailed Outcome = "failed"
)

// Session is the durable record of one UX testing run.
type Session struct {
	Target     Target
	Profile    string
	Outcome    Outcome
	Completed  []PhaseKind
	Recording  Recording
	Attachment Attachment
	Notes      []string
	StartedAt  time.Time
	FinishedAt time.Time
}

// NewSession starts a session for a plan.
func NewSession(p Plan, startedAt time.Time) (Session, error) {
	if err := p.Validate(); err != nil {
		return Session{}, err
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return Session{Target: p.Target, Profile: p.Profile, StartedAt: startedAt}, nil
}

// Complete records that a phase finished.
func (s *Session) Complete(kind PhaseKind) {
	for _, done := range s.Completed {
		if done == kind {
			return
		}
	}
	s.Completed = append(s.Completed, kind)
}

// Finish closes a session with its verdict, its recording and the uploaded
// attachment.
func (s *Session) Finish(outcome Outcome, recording Recording, attachment Attachment, finishedAt time.Time) error {
	switch outcome {
	case OutcomePassed, OutcomeFailed:
	default:
		return fmt.Errorf("uxtesting: unsupported outcome %q", outcome)
	}
	if err := recording.Validate(); err != nil {
		return err
	}
	if err := attachment.Validate(); err != nil {
		return err
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	s.Outcome = outcome
	s.Recording = recording
	s.Attachment = attachment
	s.FinishedAt = finishedAt
	return nil
}

// Duration is the wall-clock length of the run.
func (s Session) Duration() time.Duration {
	if s.FinishedAt.IsZero() || s.StartedAt.IsZero() {
		return 0
	}
	return s.FinishedAt.Sub(s.StartedAt)
}

// Comment renders the thread comment for a finished session. The recording is
// embedded as a standalone video (never a link-only reference): GitHub renders
// the <video> element inline for uploaded video attachments.
func (s Session) Comment() (string, error) {
	if err := s.Target.Validate(); err != nil {
		return "", err
	}
	if err := s.Attachment.Validate(); err != nil {
		return "", fmt.Errorf("uxtesting: cannot render a comment without the session video: %w", err)
	}
	if s.Outcome == "" {
		return "", errors.New("uxtesting: cannot render a comment for a session without an outcome")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## UX testing session — %s\n\n", s.Target.String())
	fmt.Fprintf(&b, "- Outcome: **%s**\n", s.Outcome)
	fmt.Fprintf(&b, "- Thread: %s\n", s.Target.ThreadURL())
	if ref := strings.TrimSpace(s.Target.Ref); ref != "" {
		fmt.Fprintf(&b, "- Ref under test: `%s`\n", ref)
	}
	if profile := strings.TrimSpace(s.Profile); profile != "" {
		fmt.Fprintf(&b, "- Profile: `%s`\n", profile)
	}
	if duration := s.Recording.Duration.Round(100 * time.Millisecond); duration > 0 {
		fmt.Fprintf(&b, "- Recording: %s, %s (%s)\n", duration, humanBytes(s.Attachment.Bytes), s.Attachment.MimeType)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "<video src=\"%s\" controls width=\"100%%\"></video>\n\n", s.Attachment.URL)
	fmt.Fprintf(&b, "[Download the recording (%s)](%s)\n", s.Attachment.Name, s.Attachment.URL)

	fmt.Fprintf(&b, "\n### What was tested\n\n%s\n", featureOrFallback(s.Target.Feature))
	if len(s.Completed) > 0 {
		b.WriteString("\n### Phases\n")
		for _, kind := range s.Completed {
			fmt.Fprintf(&b, "- %s\n", kind)
		}
	}
	if len(s.Notes) > 0 {
		b.WriteString("\n### Notes\n")
		for _, note := range s.Notes {
			note = strings.TrimSpace(note)
			if note == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func featureOrFallback(feature string) string {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return "Not specified: the agent exercised the change under test."
	}
	return feature
}

func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}

// SystemPrompt is the role prompt of the UX testing agent. It keeps the agent
// honest: it tests, it records, it reports, and it does not silently change
// the code under test.
const SystemPrompt = "You are the UX testing agent for a software repository. " +
	"You were mentioned on a pull request or issue thread and an operator selected you for the task. " +
	"Bring the development server for that repository up inside your sandbox - either compose the repository's " +
	"docker tooling or run the server directly - then drive the running UI with the Playwright MCP tools, " +
	"exercise the change you were asked to test, and keep Playwright's built-in session recording enabled. " +
	"When you are done, upload the recorded video as a standalone file and post it into the thread you were " +
	"mentioned in together with the phases you ran, what you exercised and anything that looked broken. " +
	"Record and report; do not fix the feature under test in the same run unless you are explicitly asked to. " +
	"Never expose secrets, tokens, kubeconfigs or user data in the recording, the screenshots or the comment."

// BotConfig returns the shared config a default UX testing agent bot is
// bootstrapped with. Integrations merge it into their bot bootstrap configs so
// the UX testing agent is one of their default agents.
func (p Profile) BotConfig() map[string]interface{} {
	tools := make([]interface{}, 0, len(PlaywrightTools))
	for _, tool := range PlaywrightTools {
		tools = append(tools, tool)
	}
	return map[string]interface{}{
		"system_prompt": SystemPrompt,
		"temperature":   0.2,
		"tools":         tools,
		"tags":          []interface{}{"ux-testing", "playwright"},
		ConfigKey: map[string]interface{}{
			"profile":              p.Name,
			"sandbox_image":        p.Sandbox.Image,
			"sandbox_workspace":    p.Sandbox.Workspace,
			"playwright_mcp":       p.Browser.MCPName,
			"browser":              p.Browser.Browser,
			"viewport":             p.Browser.Viewport,
			"record_video":         p.Browser.RecordVideo,
			"recording_dir":        p.Recording.Dir,
			"dev_server_health":    p.Server.HealthURL,
			"publish_attachment":   p.Publish.AttachmentName,
			"require_confirmation": p.Publish.RequireConfirmation,
		},
	}
}
