package uxtesting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The orchestration below is the reusable half of the UX testing agent: it
// sequences the phases of a Plan and owns the artifacts, while every effectful
// capability is an interface an integration (or a test) supplies. That keeps one
// implementation serving every repository wired to ci.msgmate.io - the
// integration only binds a sandbox, a Playwright-driven browser and a thread
// publisher.

// Sandbox is the isolated development environment the agent works in: ordinary
// repository access (checkout plus a shell) in front of the same sandbox the
// coding agents use.
type Sandbox interface {
	// Checkout prepares the workspace at the target ref inside the sandbox and
	// installs the repository dependencies.
	Checkout(ctx context.Context, target Target, spec SandboxSpec) error
	// Run executes a command in the sandbox workspace and returns its stdout.
	Run(ctx context.Context, command []string) (string, error)
	// ReadFile reads a file written inside the sandbox (the recording).
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// Teardown releases the sandbox.
	Teardown(ctx context.Context) error
}

// Browser is the Playwright MCP the agent drives. Implementations talk to the
// MCP server; recording stays enabled for the whole session.
type Browser interface {
	// Open launches the browser and starts Playwright's session recording.
	Open(ctx context.Context, spec BrowserSpec, recordingDir string) error
	// Navigate points the browser at the development server.
	Navigate(ctx context.Context, url string) error
	// Drive exercises the scenario and returns what it observed.
	Drive(ctx context.Context, scenario string) ([]string, error)
	// Close stops the recording and reports the video Playwright wrote.
	Close(ctx context.Context) (Recording, error)
}

// Publisher uploads the recording and posts the thread comment.
type Publisher interface {
	// Upload stores the recorded video as a standalone attachment.
	Upload(ctx context.Context, name, mimeType string, data []byte) (Attachment, error)
	// Comment posts the rendered session comment into the target thread.
	Comment(ctx context.Context, target Target, body string) error
}

// ErrNotConfigured is returned when the runner is missing a dependency.
var ErrNotConfigured = errors.New("uxtesting: runner is not fully configured")

// ErrRecordingMissing is returned when the browser produced no usable video.
var ErrRecordingMissing = errors.New("uxtesting: playwright produced no usable recording")

// Runner executes UX testing sessions.
type Runner struct {
	Sandbox   Sandbox
	Browser   Browser
	Publisher Publisher
	// Now defaults to time.Now.
	Now func() time.Time
	// PollInterval is the dev-server health poll interval (default 2s).
	PollInterval time.Duration
	// Logf receives progress lines; nil discards them.
	Logf func(format string, args ...interface{})
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r Runner) logf(format string, args ...interface{}) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func (r Runner) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 2 * time.Second
}

// Run executes the full plan for a target: prepare the workspace, bring the
// development server up, wait for the UI, drive it with the Playwright MCP while
// Playwright records, then publish the recording into the thread.
//
// A failure while driving the feature is not a runner error: the recording is
// still published with OutcomeFailed, because a broken feature is exactly what
// the caller wants to see. Runner errors mean the session could not produce a
// recording at all.
func (r Runner) Run(ctx context.Context, profile Profile, target Target) (Session, error) {
	if r.Sandbox == nil || r.Browser == nil || r.Publisher == nil {
		return Session{}, ErrNotConfigured
	}
	plan, err := profile.PlanFor(target)
	if err != nil {
		return Session{}, err
	}
	session, err := NewSession(plan, r.now())
	if err != nil {
		return Session{}, err
	}
	defer func() {
		if err := r.Sandbox.Teardown(context.WithoutCancel(ctx)); err != nil {
			r.logf("uxtesting: teardown %s: %v", target.String(), err)
		}
	}()

	if err := r.Sandbox.Checkout(ctx, target, profile.Sandbox); err != nil {
		return session, fmt.Errorf("uxtesting: prepare workspace for %s: %w", target.String(), err)
	}
	session.Complete(PhasePrepareWorkspace)
	r.logf("uxtesting: %s workspace prepared", target.String())

	if len(profile.Server.StartCommand) > 0 && strings.TrimSpace(profile.Server.BaseURL) == "" {
		if _, err := r.Sandbox.Run(ctx, profile.Server.StartCommand); err != nil {
			return session, fmt.Errorf("uxtesting: start dev server for %s: %w", target.String(), err)
		}
	}
	session.Complete(PhaseStartDevServer)

	healthURL := profile.Server.HealthURL
	if strings.TrimSpace(profile.Server.BaseURL) != "" {
		healthURL = profile.Server.BaseURL
	}
	if err := r.waitForServer(ctx, healthURL, profile.Server.ReadyTimeout); err != nil {
		return session, fmt.Errorf("uxtesting: wait for %s: %w", healthURL, err)
	}
	session.Complete(PhaseWaitForServer)

	if err := r.Browser.Open(ctx, profile.Browser, profile.Recording.Dir); err != nil {
		return session, fmt.Errorf("uxtesting: open playwright browser for %s: %w", target.String(), err)
	}
	if err := r.Browser.Navigate(ctx, healthURL); err != nil {
		_, _ = r.Browser.Close(context.WithoutCancel(ctx))
		return session, fmt.Errorf("uxtesting: navigate %s: %w", healthURL, err)
	}

	scenario := strings.TrimSpace(target.Feature)
	if scenario == "" {
		scenario = "exercise the change under test"
	}
	notes, driveErr := r.Browser.Drive(ctx, scenario)
	recording, closeErr := r.Browser.Close(context.WithoutCancel(ctx))
	if closeErr != nil {
		return session, fmt.Errorf("uxtesting: close playwright browser for %s: %w", target.String(), closeErr)
	}
	session.Complete(PhaseDriveUI)
	if driveErr != nil {
		notes = append(notes, "driving the feature failed: "+driveErr.Error())
	}

	if strings.TrimSpace(recording.Path) == "" {
		return session, fmt.Errorf("%w: playwright reported no video file", ErrRecordingMissing)
	}
	data, err := r.Sandbox.ReadFile(ctx, recording.Path)
	if err != nil {
		return session, fmt.Errorf("%w: read %s: %s", ErrRecordingMissing, recording.Path, err)
	}
	recording.Bytes = int64(len(data))
	if err := recording.Validate(); err != nil {
		return session, fmt.Errorf("%w: %s", ErrRecordingMissing, err)
	}
	session.Complete(PhaseCollectRecording)

	mimeType := mimeTypeForFormat(profile.Recording.Format)
	attachment, err := r.Publisher.Upload(ctx, profile.Publish.AttachmentName, mimeType, data)
	if err != nil {
		return session, fmt.Errorf("uxtesting: upload recording for %s: %w", target.String(), err)
	}
	session.Notes = append(session.Notes, notes...)

	outcome := OutcomePassed
	if driveErr != nil {
		outcome = OutcomeFailed
	}
	if err := session.Finish(outcome, recording, attachment, r.now()); err != nil {
		return session, err
	}
	body, err := session.Comment()
	if err != nil {
		return session, err
	}
	if err := r.Publisher.Comment(ctx, target, body); err != nil {
		return session, fmt.Errorf("uxtesting: publish recording for %s: %w", target.String(), err)
	}
	session.Complete(PhasePublishRecording)
	r.logf("uxtesting: %s session published (%s)", target.String(), outcome)
	return session, nil
}

// waitForServer polls the health URL through the sandbox until the UI answers
// or the timeout expires.
func (r Runner) waitForServer(ctx context.Context, healthURL string, timeout time.Duration) error {
	if strings.TrimSpace(healthURL) == "" {
		return errors.New("no health URL configured")
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := []string{"curl", "-fsS", "--max-time", "5", healthURL}
	for {
		if _, err := r.Sandbox.Run(deadlineCtx, command); err == nil {
			return nil
		}
		select {
		case <-deadlineCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("dev server did not answer within %s", timeout)
		case <-time.After(r.pollInterval()):
		}
	}
}

// mimeTypeForFormat maps a recording format to its video mime type.
func mimeTypeForFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp4":
		return "video/mp4"
	default:
		return "video/webm"
	}
}
