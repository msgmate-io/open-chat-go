package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"
)

const serviceConfigFileName = "open-chat.json"

// serviceEnvConfig is the subset of the open-chat config document written for
// the managed service. Only the `env` section is generated so secrets live in a
// 0600 file instead of the service unit or the process arguments.
type serviceEnvConfig struct {
	Env map[string]string `json:"env"`
}

// InstallCli installs the running binary to a stable location, writes a secure
// service config and registers/starts the OS service.
func InstallCli() *cli.Command {
	return &cli.Command{
		Name:  "install",
		Usage: "install the open-chat binary and register it as an OS service",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "install-dir",
				Usage: "directory the managed binary is copied to",
				Value: defaultInstallDir(),
			},
			&cli.StringFlag{
				Name:  "workdir",
				Usage: "service working directory for the database and generated config",
				Value: defaultWorkDir(),
			},
			&cli.StringFlag{
				Name:  "host",
				Usage: "server bind address",
				Value: "127.0.0.1",
			},
			&cli.Uint16Flag{
				Name:  "port",
				Usage: "server port",
				Value: 1984,
			},
			&cli.StringFlag{
				Name:  "db-path",
				Usage: "sqlite database path (defaults to <workdir>/data.db)",
			},
			&cli.StringFlag{
				Name:  "root-credentials",
				Usage: "admin credentials as username:password; generated and printed once when omitted",
			},
			&cli.StringFlag{
				Name:  "service-config",
				Usage: "path to an existing open-chat config to reference instead of generating one",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "reinstall even when a service/binary already exists (only when stopped)",
			},
			&cli.BoolFlag{
				Name:  "start",
				Usage: "start the service after installing",
				Value: true,
			},
			&cli.StringFlag{
				Name:  "user",
				Usage: "optional user account the service should run as",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runInstall(c)
		},
	}
}

func runInstall(c *cli.Command) error {
	installDir := strings.TrimSpace(c.String("install-dir"))
	if installDir == "" {
		return errors.New("--install-dir must not be empty")
	}
	workdir := strings.TrimSpace(c.String("workdir"))
	if workdir == "" {
		return errors.New("--workdir must not be empty")
	}
	host := strings.TrimSpace(c.String("host"))
	if host == "" {
		host = "127.0.0.1"
	}
	port := c.Uint16("port")
	dbPath := strings.TrimSpace(c.String("db-path"))
	if dbPath == "" {
		dbPath = filepath.Join(workdir, "data.db")
	}
	target := filepath.Join(installDir, installedBinaryName())
	force := c.Bool("force")

	if !isElevated() {
		fmt.Println("Warning: not running as root/admin; installing a system-wide service may fail")
	}

	cfg := &service.Config{
		Name:             ServiceName,
		DisplayName:      ServiceDisplayName,
		Description:      ServiceDescription,
		Executable:       target,
		WorkingDirectory: workdir,
	}
	if user := strings.TrimSpace(c.String("user")); user != "" {
		cfg.UserName = user
	}
	newService := func() (service.Service, error) {
		return service.New(&serviceProgram{}, cfg)
	}

	// Detect the service system before copying anything to the host.
	s, err := newService()
	if err != nil {
		if errors.Is(err, service.ErrNoServiceSystemDetected) {
			return errors.New("no supported service manager detected on this host; cannot install")
		}
		return err
	}

	status, statusErr := s.Status()
	switch {
	case statusErr == nil && status == service.StatusRunning:
		return fmt.Errorf("service %q is already running; stop it before installing (use --force only for a stopped service)", ServiceName)
	case statusErr == nil && status == service.StatusStopped:
		if !force {
			return fmt.Errorf("service %q is already installed; use --force to reinstall", ServiceName)
		}
	case statusErr != nil && !errors.Is(statusErr, service.ErrNotInstalled):
		// Some init systems (notably SysV) report a generic error when the
		// service is absent instead of ErrNotInstalled. Continue and let
		// Install surface a real conflict if one exists.
		fmt.Printf("Warning: could not determine service status: %v\n", statusErr)
	}

	binaryExisted := false
	if _, statErr := os.Stat(target); statErr == nil {
		binaryExisted = true
		if !force {
			return fmt.Errorf("%s already exists; use --force to overwrite", target)
		}
	}

	if version, ok := probeOpenChatVersion(host, port); ok {
		fmt.Printf("Warning: an Open Chat server is already answering on %s:%d (open-chat %s)\n", host, port, version)
	}

	serviceConfigPath := strings.TrimSpace(c.String("service-config"))
	if serviceConfigPath != "" {
		if info, statErr := os.Stat(serviceConfigPath); statErr != nil {
			return fmt.Errorf("--service-config %s is not readable: %w", serviceConfigPath, statErr)
		} else if info.IsDir() {
			return fmt.Errorf("--service-config %s is a directory, expected a file", serviceConfigPath)
		}
	}
	generatedPassword := ""
	if serviceConfigPath == "" {
		credentials := strings.TrimSpace(c.String("root-credentials"))
		if credentials == "" {
			credentials = strings.TrimSpace(os.Getenv("ROOT_CREDENTIALS"))
		}
		if credentials == "" {
			password, genErr := generateRandomPassword()
			if genErr != nil {
				return fmt.Errorf("failed to generate root credentials: %w", genErr)
			}
			credentials = "admin:" + password
			generatedPassword = password
		}
		serviceConfigPath = filepath.Join(workdir, serviceConfigFileName)
		if err := writeServiceConfigFile(serviceConfigPath, buildServiceEnv(credentials, dbPath, host, port)); err != nil {
			return err
		}
	}

	if err := installBinary(target); err != nil {
		return err
	}
	installComplete := false
	defer func() {
		if !installComplete && !binaryExisted {
			_ = os.Remove(target)
		}
	}()

	cfg.Arguments = buildServiceArguments(serviceConfigPath, host, port, dbPath)
	cfg.EnvVars = nil
	cfg.Option = service.KeyValue{"Restart": "always"}

	s, err = newService()
	if err != nil {
		return err
	}
	if force {
		// Clear any previous registration so a forced reinstall does not fail
		// with "already exists" on init systems that cannot overwrite in place.
		if uninstallErr := s.Uninstall(); uninstallErr != nil &&
			!errors.Is(uninstallErr, service.ErrNotInstalled) &&
			!errors.Is(uninstallErr, os.ErrNotExist) {
			fmt.Printf("Warning: failed to remove previous service: %v\n", uninstallErr)
		}
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("failed to install service: %w", err)
	}
	installComplete = true

	if err := ensurePath(installDir); err != nil {
		fmt.Printf("Warning: failed to update PATH: %v\n", err)
	}
	if _, err := exec.LookPath(installedBinaryName()); err != nil {
		fmt.Printf("Note: `%s` is not yet resolvable in this shell; open a new terminal or add %s to PATH\n", installedBinaryName(), installDir)
	}

	if c.Bool("start") {
		if err := s.Start(); err != nil {
			return fmt.Errorf("service installed but failed to start: %w", err)
		}
		fmt.Printf("Started service %q\n", ServiceName)
	}

	fmt.Printf("Installed %s\n", target)
	fmt.Printf("Service config: %s\n", serviceConfigPath)
	if generatedPassword != "" {
		fmt.Printf("Generated root credentials: admin:%s\n", generatedPassword)
		fmt.Println("IMPORTANT: Save this password securely; it will not be shown again.")
	}
	return nil
}

// UninstallCli stops and removes the OS service and, unless --keep-binary is
// set, the installed binary.
func UninstallCli() *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "stop and remove the open-chat OS service",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "install-dir",
				Usage: "directory the managed binary was installed to",
				Value: defaultInstallDir(),
			},
			&cli.StringFlag{
				Name:  "workdir",
				Usage: "service working directory to purge when --purge is set",
				Value: defaultWorkDir(),
			},
			&cli.BoolFlag{
				Name:  "keep-binary",
				Usage: "leave the installed binary in place",
			},
			&cli.BoolFlag{
				Name:  "purge",
				Usage: "also remove the service working directory (database and config)",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runUninstall(c)
		},
	}
}

func runUninstall(c *cli.Command) error {
	installDir := strings.TrimSpace(c.String("install-dir"))
	workdir := strings.TrimSpace(c.String("workdir"))
	target := filepath.Join(installDir, installedBinaryName())

	cfg := &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
		Executable:  target,
	}
	s, err := service.New(&serviceProgram{}, cfg)
	if err != nil {
		if errors.Is(err, service.ErrNoServiceSystemDetected) {
			return errors.New("no supported service manager detected on this host")
		}
		return err
	}

	status, statusErr := s.Status()
	installed := !errors.Is(statusErr, service.ErrNotInstalled)
	switch {
	case errors.Is(statusErr, service.ErrNotInstalled):
		fmt.Printf("Service %q is not installed\n", ServiceName)
	case statusErr != nil:
		fmt.Printf("Warning: could not determine service status: %v\n", statusErr)
	case status == service.StatusRunning:
		if err := s.Stop(); err != nil {
			fmt.Printf("Warning: failed to stop service: %v\n", err)
		}
	}

	if installed {
		if err := s.Uninstall(); err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, service.ErrNotInstalled) {
				fmt.Printf("Service %q is not installed\n", ServiceName)
			} else {
				return fmt.Errorf("failed to uninstall service: %w", err)
			}
		} else {
			fmt.Printf("Removed service %q\n", ServiceName)
		}
	}

	if !c.Bool("keep-binary") {
		switch err := os.Remove(target); {
		case err == nil:
			fmt.Printf("Removed %s\n", target)
		case os.IsNotExist(err):
		default:
			return fmt.Errorf("failed to remove %s: %w", target, err)
		}
	}
	if err := removePath(installDir); err != nil {
		fmt.Printf("Warning: failed to update PATH: %v\n", err)
	}

	if c.Bool("purge") {
		if err := os.RemoveAll(workdir); err != nil {
			return fmt.Errorf("failed to purge %s: %w", workdir, err)
		}
		fmt.Printf("Purged %s\n", workdir)
	} else {
		fmt.Printf("Kept data in %s (use --purge to remove it)\n", workdir)
	}
	return nil
}

// StatusCli reports the OS service state and whether an open-chat server
// answers on the configured host:port.
func StatusCli() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "report the open-chat service state and server version",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "install-dir",
				Usage: "directory the managed binary was installed to",
				Value: defaultInstallDir(),
			},
			&cli.StringFlag{
				Name:  "host",
				Usage: "server bind address to probe",
				Value: "127.0.0.1",
			},
			&cli.Uint16Flag{
				Name:  "port",
				Usage: "server port to probe",
				Value: 1984,
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runStatus(c)
		},
	}
}

func runStatus(c *cli.Command) error {
	installDir := strings.TrimSpace(c.String("install-dir"))
	target := filepath.Join(installDir, installedBinaryName())

	serviceState := "unknown"
	cfg := &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
		Executable:  target,
	}
	if s, err := service.New(&serviceProgram{}, cfg); err != nil {
		if errors.Is(err, service.ErrNoServiceSystemDetected) {
			serviceState = "unavailable (no service manager detected)"
		} else {
			return err
		}
	} else {
		status, statusErr := s.Status()
		switch {
		case errors.Is(statusErr, service.ErrNotInstalled):
			serviceState = "not installed"
		case statusErr != nil:
			serviceState = fmt.Sprintf("unknown (%v)", statusErr)
		case status == service.StatusRunning:
			serviceState = "running"
		case status == service.StatusStopped:
			serviceState = "stopped"
		}
	}

	binaryState := "missing"
	if _, err := os.Stat(target); err == nil {
		binaryState = target
	}

	host := strings.TrimSpace(c.String("host"))
	port := c.Uint16("port")
	fmt.Printf("service: %s\n", serviceState)
	fmt.Printf("binary:  %s\n", binaryState)
	if version, ok := probeOpenChatVersion(host, port); ok {
		fmt.Printf("server:  running on %s:%d (open-chat %s)\n", host, port, version)
	} else {
		fmt.Printf("server:  not answering on %s:%d\n", host, port)
	}
	return nil
}

// buildServiceArguments renders the argument vector the service manager uses to
// start the server. The global --config flag must precede the subcommand so the
// root config bootstrap can read it before cli parsing.
func buildServiceArguments(configPath string, host string, port uint16, dbPath string) []string {
	args := make([]string, 0, 10)
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "--config", configPath)
	}
	args = append(args, "service", "run", "--host", host, "--port", strconv.Itoa(int(port)))
	if strings.TrimSpace(dbPath) != "" {
		args = append(args, "--db-path", dbPath)
	}
	return args
}

// buildServiceEnv renders the env section persisted in the generated service
// config. Keeping credentials here avoids leaking them into unit files or `ps`.
func buildServiceEnv(rootCredentials string, dbPath string, host string, port uint16) map[string]string {
	return map[string]string{
		"ROOT_CREDENTIALS": rootCredentials,
		"DB_PATH":          dbPath,
		"HOST":             host,
		"PORT":             strconv.Itoa(int(port)),
	}
}

// writeServiceConfigFile writes the service config atomically with restrictive
// permissions (file 0600, parent directory 0700).
func writeServiceConfigFile(path string, env map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create service config directory %s: %w", dir, err)
	}

	payload, err := json.MarshalIndent(serviceEnvConfig{Env: env}, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	tmp, err := os.CreateTemp(dir, ".open-chat-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write service config %s: %w", path, err)
	}
	return nil
}

// installBinary copies the current executable to target atomically, skipping
// the copy when the running binary already is the installed one.
func installBinary(target string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve current executable: %w", err)
	}
	srcInfo, err := os.Stat(exe)
	if err != nil {
		return fmt.Errorf("failed to stat current executable: %w", err)
	}
	if dstInfo, statErr := os.Stat(target); statErr == nil && os.SameFile(srcInfo, dstInfo) {
		return nil
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create install directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".open-chat-install-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	src, err := os.Open(exe)
	if err != nil {
		tmp.Close()
		return err
	}
	defer src.Close()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("failed to install binary to %s: %w", target, err)
	}
	return nil
}
