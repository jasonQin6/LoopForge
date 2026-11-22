package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversationRunnerLoginUsesOnlyNativeAuthInIsolatedHome(t *testing.T) {
	for _, provider := range []string{"codex", "claude-code"} {
		t.Run(provider, func(t *testing.T) {
			if provider == "codex" {
				if _, err := exec.LookPath("python3"); err != nil {
					t.Skip("python3 unavailable")
				}
			}
			login, _ := runnerLoginSpec(provider)
			host := t.TempDir()
			root := t.TempDir()
			t.Setenv(login.configEnv, host)
			t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
			t.Setenv("ANTHROPIC_API_KEY", "host-key")
			t.Setenv("OPENAI_API_KEY", "host-key")
			file := filepath.Join(host, login.file)
			if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(host, "settings.json"), []byte("host settings"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pool := &ConversationProcessPool{}
			script := `set -eu
test -f "$HOME/$1/$2"
test ! -e "$HOME/$1/settings.json"
test -z "${OPENAI_API_KEY:-}${CODEX_API_KEY:-}${ANTHROPIC_API_KEY:-}"
printf 'local-auth-ok\n'`
			req := ConversationProcessRequest{Operation: "start", Command: &ConversationCommand{
				Argv: []string{"sh", "-c", script, "runner-login", login.dir, login.file}, Provider: provider,
				Bindings: []CredentialBinding{login.binding}, RunnerLogin: true,
				Credentials: RunCredentials{Codex: ProviderCredential{APIKey: "personal"}, ClaudeCode: ProviderCredential{APIKey: "personal"}},
			}}
			started, err := pool.Handle(ctx, root, "", nil, req, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := conversationPoll(t, pool, ctx, root, started.ID); strings.TrimSpace(got) != "local-auth-ok" {
				t.Fatalf("isolated CLI did not see expected login state: %q", got)
			}
			if _, err := pool.Handle(ctx, root, "", nil, ConversationProcessRequest{Operation: "stop", ID: started.ID}, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "home", login.dir, login.file)); !os.IsNotExist(err) {
				t.Fatalf("local credential copy was not removed: %v", err)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "{}" {
				t.Fatal("native CLI login file changed")
			}
			stale := filepath.Join(root, "home", login.dir, login.file)
			if err := os.WriteFile(stale, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			req.Command.RunnerLogin = false
			req.Command.Credentials = RunCredentials{}
			if _, err := pool.Handle(ctx, root, "", nil, req, nil); err == nil || !strings.Contains(err.Error(), "API Key") {
				t.Fatalf("personal mode fell back to runner login: %v", err)
			}
			if _, err := os.Stat(stale); !os.IsNotExist(err) {
				t.Fatal("personal mode retained a stale login file")
			}
			req.Command.RunnerLogin = true

			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Handle(ctx, root, "", nil, req, nil); err == nil || !strings.Contains(err.Error(), "本机登录态不可用") {
				t.Fatalf("missing native login did not report a runner-specific error: %v", err)
			}
		})
	}
}

func TestConversationRunnerLoginSupportsClaudeOAuthEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "runner-fixture")
	req := ConversationProcessRequest{Operation: "start", Command: &ConversationCommand{
		Provider: "claude-code", Bindings: []CredentialBinding{BindClaudeCode}, RunnerLogin: true,
		Argv: []string{"sh", "-c", `test "$CLAUDE_CODE_OAUTH_TOKEN" = runner-fixture && test ! -e "$CLAUDE_CONFIG_DIR/.credentials.json" && printf 'ok\n'`},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool := &ConversationProcessPool{}
	started, err := pool.Handle(ctx, root, "", nil, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationPoll(t, pool, ctx, root, started.ID); strings.TrimSpace(got) != "ok" {
		t.Fatal("CLI did not use the Runner OAuth environment")
	}
}

func TestCodeBuddyLocalLoginReusesHostHome(t *testing.T) {
	login, supported := runnerLoginSpec("codebuddy")
	if !supported || login.binding != BindCodeBuddy || login.file != "" {
		t.Fatal("codebuddy runner login spec missing or malformed")
	}
	// docker 对话沙箱看不到宿主 home,必须明确拒绝而不是静默走错目录。
	if _, _, err := prepareConversationRunnerLogin(t.TempDir(), login, true, true); err == nil || !strings.Contains(err.Error(), "docker") {
		t.Fatalf("docker sandbox was not rejected: %v", err)
	}
	// 沙箱任务路径(seatbelt/bwrap/docker)不支持真实 HOME,必须拒绝。
	if _, _, err := PrepareRunnerSandboxLogin(nil, "codebuddy", "r", "n"); err == nil || !strings.Contains(err.Error(), "沙箱") {
		t.Fatalf("sandboxed task was not rejected: %v", err)
	}
	// 宿主没有 ~/.codebuddy 时给出 runner 侧错误。
	bare := t.TempDir()
	t.Setenv("HOME", bare)
	if _, _, err := prepareConversationRunnerLogin(t.TempDir(), login, true, false); err == nil || !strings.Contains(err.Error(), "本机登录态不可用") {
		t.Fatalf("missing host login was not reported: %v", err)
	}
	if cleanup, env, err := prepareConversationRunnerLogin(t.TempDir(), login, false, false); cleanup != nil || env != nil || err != nil {
		t.Fatal("personal mode must not touch the host home")
	}

	host := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", host)
	configDir := filepath.Join(host, ".codebuddy")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	settings := `{"model":"hy4flash-free"}`
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEBUDDY_API_KEY", "host-key")
	script := `set -eu
test "$HOME" = "$1"
test -z "${CODEBUDDY_API_KEY:-}"
test -z "${CODEBUDDY_BASE_URL:-}"
grep -q hy4flash-free "$HOME/.codebuddy/settings.json"
printf 'local-auth-ok\n'`
	req := ConversationProcessRequest{Operation: "start", Command: &ConversationCommand{
		Argv: []string{"sh", "-c", script, "codebuddy-login", host}, Provider: "codebuddy",
		Bindings: []CredentialBinding{BindCodeBuddy}, RunnerLogin: true,
		Credentials: RunCredentials{CodeBuddy: CodeBuddyCredential{APIKey: "personal"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool := &ConversationProcessPool{}
	started, err := pool.Handle(ctx, root, "", nil, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationPoll(t, pool, ctx, root, started.ID); strings.TrimSpace(got) != "local-auth-ok" {
		t.Fatalf("CLI did not reuse the host home: %q", got)
	}
	if _, err := pool.Handle(ctx, root, "", nil, ConversationProcessRequest{Operation: "stop", ID: started.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(configDir, "settings.json")); err != nil || string(data) != settings {
		t.Fatal("host codebuddy settings changed")
	}
	req.Command.RunnerLogin = false
	req.Command.Credentials = RunCredentials{}
	if _, err := pool.Handle(ctx, root, "", nil, req, nil); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("personal mode without a key fell back to runner login: %v", err)
	}
}
