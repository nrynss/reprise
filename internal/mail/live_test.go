//go:build live

package mail

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSendLiveProbe sends one real message through Resend and records the
// provider id. It never runs in CI. The key, the sender, and the
// destination arrive through the environment. Pass them as
// command-prefixed values and never export them, so no secret lands in
// shell history.
func TestSendLiveProbe(t *testing.T) {
	key := os.Getenv("REPRISE_MAIL_LIVE_KEY")
	from := os.Getenv("REPRISE_MAIL_LIVE_FROM")
	to := os.Getenv("REPRISE_MAIL_LIVE_TO")
	if key == "" || from == "" || to == "" {
		t.Skip("set REPRISE_MAIL_LIVE_KEY, REPRISE_MAIL_LIVE_FROM, and REPRISE_MAIL_LIVE_TO to run the live probe")
	}
	client, err := New(Config{
		APIKey:  key,
		From:    from,
		ReplyTo: os.Getenv("REPRISE_MAIL_LIVE_REPLY_TO"),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stamp := time.Now().UTC().Format(time.RFC3339)
	got, err := client.Send(ctx, Message{
		To:      to,
		Subject: "Reprise live probe",
		Text:    "This is a one-off delivery probe sent at " + stamp + ". No action is needed.",
	})
	if err != nil {
		t.Fatalf("live send: %v", err)
	}
	if got.ID == "" {
		t.Fatal("live send returned an empty provider id")
	}
	t.Logf("live probe provider id: %s", got.ID)
}
