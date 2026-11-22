package httpapi

import (
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

func TestCodeBuddyRunnerLoginTurnNeedsNoPlatformKey(t *testing.T) {
	h, _ := workbenchFixture(t)
	if err := h.repo.PutUser(domain.User{LoginName: "cb-keyless"}); err != nil {
		t.Fatal(err)
	}
	owner, _ := h.repo.UserByLoginName("cb-keyless")
	if _, model, refusal := h.assistantTurnUser(owner, "codebuddy:hy4flash-free", false); refusal == "" {
		t.Fatalf("keyless personal turn was accepted: model=%q", model)
	}
	user, model, refusal := h.assistantTurnUser(owner, "codebuddy:hy4flash-free", true)
	if refusal != "" || model != "codebuddy:hy4flash-free" || user.ID != owner.ID {
		t.Fatalf("runner-login turn was refused: user=%d model=%q refusal=%q", user.ID, model, refusal)
	}
	// A configured shared key must not force its model allowlist onto a
	// runner-login turn: the account logged in on the machine decides.
	if err := h.repo.SetPlatformCodeBuddyKey(repo.PlatformCodeBuddyKeyUpdate{APIKey: "shared", Models: []string{"deepseek-v4.1-flash"}}, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, model, refusal := h.assistantTurnUser(owner, "codebuddy:hy4flash-free", true); refusal != "" || model != "codebuddy:hy4flash-free" {
		t.Fatalf("shared key constrained a runner-login turn: model=%q refusal=%q", model, refusal)
	}
	if _, _, refusal := h.assistantTurnUser(owner, "codebuddy:hy4flash-free", false); refusal == "" {
		t.Fatal("personal turn skipped the shared-key model allowlist")
	}
}
