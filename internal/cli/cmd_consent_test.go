package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/testutil"
)

const (
	safeKey  = "x/safe@1"
	recKey   = "x/rec@1"
	callKey  = "y/call@1"
	neverKey = "z/never@1"
)

// consentFixture sets up a temp home with one rule per label and the given consent keys (nil: setup never ran).
func consentFixture(t *testing.T, configBody string, consented []string) *state.Store {
	t.Helper()
	testutil.TempHome(t)
	withTerminal(t, false, "")
	old := consentRules
	t.Cleanup(func() { consentRules = old })
	consentRules = func(config.Paths) ([]core.Rule, []error) {
		return []core.Rule{
			{Pack: "x", ID: "safe", Rev: 1, Category: "temp", Bucket: core.Auto},
			{Pack: "x", ID: "rec", Rev: 1, Category: "deps", Bucket: core.Optional, Recommend: true},
			{Pack: "y", ID: "call", Rev: 1, Category: "worktrees", Bucket: core.Optional},
			{Pack: "z", ID: "never", Rev: 1, Category: "models", Bucket: core.Never},
		}, nil
	}
	if configBody != "" {
		p, err := config.ResolvePaths()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p.Config), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Config, []byte(configBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if consented != nil {
		if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: consented}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func consentedKeys(t *testing.T, st *state.Store) []string {
	t.Helper()
	c, err := st.Consent()
	if err != nil || c == nil {
		t.Fatalf("consent: %v %v", c, err)
	}
	return c.Rules
}

func consentJSON(t *testing.T, args ...string) (waiting []string, labels map[string]string) {
	t.Helper()
	code, out, e := runCLI(t, append([]string{"consent", "--json"}, args...)...)
	var doc struct {
		Waiting []string          `json:"waiting"`
		Labels  map[string]string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("%d %q %q %v", code, out, e, err)
	}
	return doc.Waiting, doc.Labels
}

func TestConsentListsWaitingWithLabels(t *testing.T) {
	st := consentFixture(t, "", []string{})
	code, out, e := runCLI(t, "consent")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	for _, want := range []string{safeKey + "  safe", recKey + "  recommended", "consent --yes", "--recommended"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, neverKey) {
		t.Errorf("never rules do not wait: %q", out)
	}
	// your-call rules are a group of their own, after the others, with the one-key command
	_, group, found := strings.Cut(out, "your call rules")
	if !found || !strings.Contains(group, callKey) || !strings.Contains(group, "sapulidi consent --yes <rule-key>") ||
		strings.Contains(group, recKey) || !strings.Contains(out, "2 rule(s)") {
		t.Errorf("your-call group: %q", out)
	}
	waiting, labels := consentJSON(t)
	if !slices.Equal(waiting, []string{recKey, safeKey, callKey}) || labels[recKey] != "recommended" ||
		labels[safeKey] != "safe" || labels[callKey] != "your-call" {
		t.Fatalf("%v %v", waiting, labels)
	}
	if len(consentedKeys(t, st)) != 0 {
		t.Fatal("listing must record nothing")
	}
}

func TestConsentYesConfirmsSafeOnly(t *testing.T) {
	st := consentFixture(t, "", []string{})
	code, out, e := runCLI(t, "consent", "--yes")
	if code != 0 || !strings.Contains(out, "consent recorded for 1 rules") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{safeKey}) {
		t.Fatalf("%v", got)
	}
	code, out, _ = runCLI(t, "consent")
	if code != 0 || !strings.Contains(out, "no rules are waiting") || !strings.Contains(out, recKey+"  recommended") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestConsentYesRecommendedNeedsTerminal(t *testing.T) {
	st := consentFixture(t, "", []string{})
	code, _, e := runCLI(t, "consent", "--yes", "--recommended")
	if code != 1 || !strings.Contains(e, "run this in your own terminal: sapulidi consent --yes --recommended") {
		t.Fatalf("no tty: %d %q", code, e)
	}
	withTerminal(t, true, "no\n")
	if code, _, _ := runCLI(t, "consent", "--yes", "--recommended"); code != 1 {
		t.Fatalf("mismatch: %d", code)
	}
	if got := consentedKeys(t, st); len(got) != 0 {
		t.Fatalf("a refused consent recorded %v", got)
	}
}

func TestConsentYesRecommended(t *testing.T) {
	st := consentFixture(t, "", []string{})
	tty := recordingTTY(t, "allow\n")
	code, out, e := runCLI(t, "consent", "--yes", "--recommended")
	if got := tty.String(); !strings.Contains(got, recKey) || strings.Contains(got, safeKey) {
		t.Fatalf("the question names the recommended rules only: %q", got)
	}
	if code != 0 || !strings.Contains(out, "consent recorded for 2 rules") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{recKey, safeKey}) {
		t.Fatalf("%v", got)
	}
}

func TestConsentUpgradeEnabledCategoryWaits(t *testing.T) {
	body := "[category.worktrees]\nenabled = true\n"
	st := consentFixture(t, body, []string{safeKey})
	waiting, labels := consentJSON(t)
	if !slices.Equal(waiting, []string{recKey, callKey}) || labels[callKey] != "your-call" {
		t.Fatalf("%v %v", waiting, labels)
	}
	withTerminal(t, true, "allow\n")
	code, out, _ := runCLI(t, "consent", "--yes", "--recommended")
	if code != 0 || !strings.Contains(out, "consent recorded for 1 rules") || !strings.Contains(out, callKey+"  your call") {
		t.Fatalf("a your-call rule is never confirmed here: %d %q", code, out)
	}
	if got := consentedKeys(t, st); slices.Contains(got, callKey) {
		t.Fatalf("%v", got)
	}
}

func TestConsentHiddenCategoryYourCallDoesNotWait(t *testing.T) {
	consentFixture(t, "[category.worktrees]\nenabled = false\n", []string{})
	if waiting, _ := consentJSON(t); !slices.Equal(waiting, []string{recKey, safeKey}) {
		t.Fatalf("%v", waiting)
	}
}

func TestConsentYesYourCallKeyNeedsTerminal(t *testing.T) {
	st := consentFixture(t, "", []string{})
	for _, args := range [][]string{{callKey}, {safeKey, callKey}} {
		code, _, e := runCLI(t, append([]string{"consent", "--yes"}, args...)...)
		if code != 1 || !strings.Contains(e, "run this in your own terminal: sapulidi consent --yes") {
			t.Fatalf("no tty %v: %d %q", args, code, e)
		}
	}
	withTerminal(t, true, "no\n")
	if code, _, _ := runCLI(t, "consent", "--yes", callKey); code != 1 {
		t.Fatalf("mismatch: %d", code)
	}
	if got := consentedKeys(t, st); len(got) != 0 {
		t.Fatalf("a refused consent recorded %v", got)
	}
	withTerminal(t, true, "allow\n")
	code, out, e := runCLI(t, "consent", "--yes", callKey)
	if code != 0 || !strings.Contains(out, "consent recorded for 1 rules") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{callKey}) {
		t.Fatalf("exactly the named key: %v", got)
	}
}

func TestConsentYesSafeKeyNeedsNoTerminal(t *testing.T) {
	st := consentFixture(t, "", []string{}) // no terminal: today's tier
	if code, out, e := runCLI(t, "consent", "--yes", safeKey); code != 0 {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{safeKey}) {
		t.Fatalf("%v", got)
	}
}

func TestConsentYesRecommendedKeyNeedsTerminal(t *testing.T) {
	st := consentFixture(t, "", []string{})
	for _, args := range [][]string{{recKey}, {safeKey, recKey}} {
		code, _, e := runCLI(t, append([]string{"consent", "--yes"}, args...)...)
		retry := "run this in your own terminal: sapulidi consent --yes " + strings.Join(args, " ")
		if code != 1 || !strings.Contains(e, retry) {
			t.Fatalf("no tty %v: %d %q", args, code, e)
		}
	}
	withTerminal(t, true, "no\n")
	if code, _, _ := runCLI(t, "consent", "--yes", recKey); code != 1 {
		t.Fatalf("mismatch: %d", code)
	}
	if got := consentedKeys(t, st); len(got) != 0 {
		t.Fatalf("a refused consent recorded %v", got)
	}
	withTerminal(t, true, "allow\n")
	code, out, e := runCLI(t, "consent", "--yes", recKey, safeKey, recKey)
	if code != 0 || !strings.Contains(out, "consent recorded for 2 rules") {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{recKey, safeKey}) {
		t.Fatalf("%v", got)
	}
}

func TestConsentYesKeyThatDoesNotWait(t *testing.T) {
	st := consentFixture(t, "", []string{safeKey})
	for _, key := range []string{safeKey, neverKey, "x/unknown@1", "x/rec@2"} {
		code, _, e := runCLI(t, "consent", "--yes", recKey, key)
		if code != 1 || !strings.Contains(e, "not waiting") {
			t.Errorf("%s: %d %q", key, code, e)
		}
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{safeKey}) {
		t.Fatalf("%v", got)
	}
}

func TestConsentJSONListsConfirmed(t *testing.T) {
	consentFixture(t, "", []string{})
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{}},
		{[]string{"--yes"}, []string{safeKey}},
		{[]string{"--yes", recKey}, []string{recKey}},
	} {
		withTerminal(t, true, "allow\n") // the recommended key asks
		code, out, e := runCLI(t, append([]string{"consent", "--json"}, tc.args...)...)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
			t.Fatalf("%v: %d %q %q %v", tc.args, code, out, e, err)
		}
		var confirmed []string
		if err := json.Unmarshal(doc["confirmed"], &confirmed); err != nil || confirmed == nil || !slices.Equal(confirmed, tc.want) {
			t.Errorf("%v: confirmed %s, want %v", tc.args, doc["confirmed"], tc.want)
		}
		for _, k := range []string{"waiting", "labels", "recorded"} {
			if _, ok := doc[k]; !ok {
				t.Errorf("%v: field %q is gone", tc.args, k)
			}
		}
	}
}

func TestConsentCorruptConfigRefuses(t *testing.T) {
	st := consentFixture(t, "low_disk_gb = [", []string{safeKey})
	withTerminal(t, true, "allow\n")
	for _, args := range [][]string{{"--yes"}, {"--yes", "--recommended"}, {"--yes", callKey}, {"--revoke", safeKey},
		{"--revoke", "recommended"}} {
		withTerminal(t, true, "revoke\n")
		code, out, e := runCLI(t, append([]string{"consent"}, args...)...)
		if code != 1 || !strings.Contains(e, "not valid TOML") || strings.Contains(out, "consent re") {
			t.Errorf("%v: %d %q %q", args, code, out, e)
		}
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{safeKey}) {
		t.Fatalf("a corrupt config changed consent: %v", got)
	}
	code, out, _ := runCLI(t, "consent")
	if code != 0 || !strings.Contains(out, "not valid TOML") || strings.Contains(out, recKey) ||
		strings.Contains(out, "no rules are waiting") {
		t.Fatalf("listing: %d %q", code, out)
	}
	if waiting, _ := consentJSON(t); len(waiting) != 0 {
		t.Fatalf("%v", waiting)
	}
}

func TestConsentRevokeKey(t *testing.T) {
	st := consentFixture(t, "", []string{recKey, safeKey})

	code, _, e := runCLI(t, "consent", "--revoke", safeKey)
	if code != 1 || !strings.Contains(e, "run this in your own terminal: sapulidi consent --revoke "+safeKey) {
		t.Fatalf("no tty: %d %q", code, e)
	}
	withTerminal(t, true, "no\n")
	if code, _, _ := runCLI(t, "consent", "--revoke", safeKey); code != 1 {
		t.Fatalf("mismatch: %d", code)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{recKey, safeKey}) {
		t.Fatalf("a refused revoke changed consent: %v", got)
	}
	withTerminal(t, true, "revoke\n")
	if code, out, e := runCLI(t, "consent", "--revoke", safeKey); code != 0 {
		t.Fatalf("revoke: %d %q %q", code, out, e)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{recKey}) {
		t.Fatalf("%v", got)
	}
	withTerminal(t, true, "revoke\n")
	if code, _, _ := runCLI(t, "consent", "--revoke", safeKey); code != 1 {
		t.Fatalf("revoking a key that is not consented must fail: %d", code)
	}
}

func TestConsentRevokeRecommended(t *testing.T) {
	st := consentFixture(t, "", []string{recKey, safeKey})
	if code, _, _ := runCLI(t, "consent", "--revoke", "recommended"); code != 1 {
		t.Fatalf("no tty: %d", code)
	}
	withTerminal(t, true, "revoke\n")
	code, out, e := runCLI(t, "consent", "--revoke", "recommended", "--json")
	var doc struct {
		Revoked []string `json:"revoked"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 || !slices.Equal(doc.Revoked, []string{recKey}) {
		t.Fatalf("%d %q %q %v", code, out, e, err)
	}
	if got := consentedKeys(t, st); !slices.Equal(got, []string{safeKey}) {
		t.Fatalf("%v", got)
	}
}

func TestConsentRevokeBeforeSetupAndBadFlags(t *testing.T) {
	consentFixture(t, "", nil)
	withTerminal(t, true, "revoke\n")
	if code, _, e := runCLI(t, "consent", "--revoke", safeKey); code != 1 || !strings.Contains(e, "not set up") {
		t.Fatalf("%d %q", code, e)
	}
	for _, args := range [][]string{{"--recommended"}, {"--yes", "--revoke", safeKey}, {"--revoke", ""},
		{safeKey}, {"--yes", "--recommended", recKey}, {"--yes", recKey, "--recommended"}} {
		if code, _, _ := runCLI(t, append([]string{"consent"}, args...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
