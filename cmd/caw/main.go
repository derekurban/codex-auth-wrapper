package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/derekurban/codex-auth-wrapper/internal/broker"
	"github.com/derekurban/codex-auth-wrapper/internal/codex"
	"github.com/derekurban/codex-auth-wrapper/internal/homeui"
	"github.com/derekurban/codex-auth-wrapper/internal/host"
	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
	"github.com/derekurban/codex-auth-wrapper/internal/store"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	paths, err := store.DefaultPaths()
	if err != nil {
		return err
	}
	switch {
	case len(args) >= 1 && (args[0] == "--version" || args[0] == "-V" || args[0] == "version"):
		printVersion()
		return nil
	case len(args) >= 1 && args[0] == "shutdown":
		return stopBroker(paths)
	case len(args) >= 2 && args[0] == "internal" && args[1] == "broker":
		return runBroker(paths)
	case len(args) >= 1 && args[0] == "status":
		return runStatus(paths)
	case len(args) >= 2 && args[0] == "broker" && args[1] == "start":
		return ensureBroker(paths)
	case len(args) >= 2 && args[0] == "broker" && args[1] == "stop":
		return stopBroker(paths)
	case len(args) >= 2 && args[0] == "broker" && args[1] == "restart":
		if err := stopBroker(paths); err != nil {
			return err
		}
		return ensureBroker(paths)
	default:
		return runHost(paths)
	}
}

func printVersion() {
	fmt.Printf("caw %s\ncommit=%s\ndate=%s\n", version, commit, date)
}

func runBroker(paths store.Paths) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := broker.New(paths)
	return svc.Run(ctx)
}

func runStatus(paths store.Paths) error {
	client, err := ensureClient(paths, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var snapshot ipc.StatusSnapshot
	if err := client.Request(ctx, "status.snapshot", ipc.Empty{}, &snapshot); err != nil {
		return err
	}
	fmt.Printf("Broker state: %s\n", snapshot.BrokerState)
	fmt.Printf("Active epoch: %s\n", snapshot.ActiveAuthEpochID)
	if snapshot.ActiveProfileID != nil {
		fmt.Printf("Active profile: %s\n", *snapshot.ActiveProfileID)
	} else {
		fmt.Println("Active profile: none")
	}
	fmt.Printf("Sessions: %d\n", snapshot.SessionCount)
	fmt.Printf("Server state: %s\n", snapshot.ServerState)
	if snapshot.ServerURL != nil {
		fmt.Printf("Server URL: %s\n", *snapshot.ServerURL)
	}
	return nil
}

func stopBroker(paths store.Paths) error {
	client, err := ensureClient(paths, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return client.Request(ctx, "broker.stop", ipc.Empty{}, nil)
}

func unregisterSession(client *ipc.Client, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = client.Request(ctx, "session.unregister", ipc.UnregisterSessionRequest{SessionID: sessionID}, nil)
}

func runHost(paths store.Paths) error {
	signals := &host.SignalBuffer{}
	client, err := ensureClient(paths, signals.HandleEvent)
	if err != nil {
		return err
	}
	defer client.Close()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	sessionID := "sess-" + uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Request(ctx, "session.register", ipc.RegisterSessionRequest{
		SessionID: sessionID,
		Cwd:       cwd,
	}, nil); err != nil {
		return err
	}
	defer unregisterSession(client, sessionID)

	hostRuntime := host.NewSessionRuntime(client, paths, signals, sessionID, cwd, clearTerminal)

	statusMessage := ""
	for {
		action, err := homeui.Run(client, signals.TakeHomeEvent, sessionID, statusMessage)
		if err != nil {
			return err
		}
		switch action.Type {
		case homeui.ActionQuit:
			return nil
		case homeui.ActionAddProfile:
			message, err := addProfileFlow(client, action)
			if err != nil {
				statusMessage = "Account linking failed: " + err.Error()
			} else {
				statusMessage = message
			}
		case homeui.ActionContinue:
			message, err := hostRuntime.EnterCodex()
			if err != nil {
				statusMessage = "Codex launch failed: " + err.Error()
			} else {
				statusMessage = message
			}
		default:
			return nil
		}
	}
}

func addProfileFlow(client *ipc.Client, action homeui.Action) (string, error) {
	tempHome, err := os.MkdirTemp("", "caw-login-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempHome)
	fmt.Println()
	fmt.Println("Codex Auth Wrapper is handing off to stock `codex login`.")
	fmt.Println("Complete the login flow in Codex. When it exits, the wrapper will import the resulting auth.json.")
	fmt.Println()
	if err := codex.RunLogin(tempHome); err != nil {
		return "", err
	}
	authPath := filepath.Join(tempHome, "auth.json")
	if _, err := os.Stat(authPath); err != nil {
		return "", fmt.Errorf("expected auth file at %s", authPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Request(ctx, "profile.add", ipc.AddProfileRequest{
		ID:       action.ProfileID,
		Name:     action.ProfileName,
		AuthPath: authPath,
	}, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Linked account %q.", action.ProfileName), nil
}

func clearTerminal() {
	fmt.Print("\x1b[2J\x1b[H")
}

func ensureClient(paths store.Paths, handler ipc.EventHandler) (*ipc.Client, error) {
	if err := ensureBroker(paths); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ipc.Dial(ctx, 5*time.Second, handler)
}

func ensureBroker(paths store.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	client, err := ipc.Dial(ctx, 300*time.Millisecond, nil)
	if err == nil {
		_ = client.Close()
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "internal", "broker")
	cmd.Env = os.Environ()
	devNull, openErr := os.OpenFile(os.DevNull, os.O_RDWR, 0o644)
	if openErr == nil {
		defer devNull.Close()
		cmd.Stdin = devNull
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}
	cmd.SysProcAttr = brokerSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		time.Sleep(150 * time.Millisecond)
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		client, dialErr := ipc.Dial(dialCtx, 250*time.Millisecond, nil)
		dialCancel()
		if dialErr == nil {
			_ = client.Close()
			return nil
		}
	}
	return fmt.Errorf("broker did not become ready")
}
