package guard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

func TestSecretFindsEnvDeep(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "a", "b", "c", ".env"), "K=v")
	err := Secret(d)
	wantWhy(t, err, "secret")
	if got := err.(*core.KeepError).Detail; got != filepath.Join("a", "b", "c", ".env") {
		t.Fatalf("detail %q", got)
	}
	for _, name := range []string{".env.local", "x.pem", "x.KEY", "a.p12", "a.pfx", "id_rsa", "id_rsa.pub", "id_ed25519",
		"id_ecdsa", "vault.kdbx", "credentials", "credentials.json", "terraform.tfstate", "terraform.tfstate.backup"} {
		d := t.TempDir()
		write(t, filepath.Join(d, "sub", name), "x")
		wantWhy(t, Secret(d), "secret")
	}
}

func TestSecretIgnoresSymlinkTarget(t *testing.T) {
	d := t.TempDir()
	out := t.TempDir()
	write(t, filepath.Join(out, ".env"), "K=v")
	write(t, filepath.Join(d, "ok.txt"), "x")
	if err := os.Symlink(out, filepath.Join(d, "ln")); err != nil {
		t.Fatal(err)
	}
	wantWhy(t, Secret(d), "")
}

func TestSecretNoneIsNil(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "a", "main.go"), "x")
	write(t, filepath.Join(d, "env.txt"), "x")
	wantWhy(t, Secret(d), "")
	wantWhy(t, Secret(filepath.Join(d, "missing")), "error")
}

func TestSecretSkipsPackageSubtrees(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, ".venv", "lib", "python3.12", "site-packages", "certifi", "cacert.pem"), "x")
	write(t, filepath.Join(d, "node_modules", "pkg", "test", "k.pem"), "x")
	write(t, filepath.Join(d, "node_modules", "@s", "pkg", "k.key"), "x")
	write(t, filepath.Join(d, "vendor", "bundle", "gems", "x", "a.pem"), "x")
	write(t, filepath.Join(d, ".cargo", "registry", "a.pem"), "x")
	write(t, filepath.Join(d, "go", "pkg", "mod", "x", "a.pem"), "x")
	wantWhy(t, Secret(d), "")
	write(t, filepath.Join(d, ".venv", ".env"), "x")
	wantWhy(t, Secret(d), "secret")
	// the candidate itself is node_modules: its packages are still skipped
	nm := filepath.Join(d, "node_modules")
	wantWhy(t, Secret(nm), "")
	write(t, filepath.Join(nm, ".env"), "x")
	wantWhy(t, Secret(nm), "secret")
}

func TestNoCtimeNeedsTestFlag(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "f")
	testutil.Touch(t, f, 48*time.Hour)
	testutil.Touch(t, d, 48*time.Hour)
	now := time.Now()
	t.Setenv("SAPULIDI_TEST", "")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	wantWhy(t, Fresh(d, time.Hour, now), "young") // ctime is recent, flag alone is ignored
	t.Setenv("SAPULIDI_TEST", "1")
	wantWhy(t, Fresh(d, time.Hour, now), "")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "")
	wantWhy(t, Fresh(d, time.Hour, now), "young")
}

func TestSecretCandidateItself(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"id_rsa", "server.pem", ".env"} {
		write(t, filepath.Join(d, n), "x")
		wantWhy(t, Secret(filepath.Join(d, n)), "secret")
	}
}

func TestSecretCargoOnlyRegistrySkipped(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "proj", ".cargo", "registry", "a.pem"), "x")
	write(t, filepath.Join(d, "proj", ".cargo", "git", "b.key"), "x")
	wantWhy(t, Secret(d), "")
	write(t, filepath.Join(d, "proj", ".cargo", "credentials.toml"), "x")
	wantWhy(t, Secret(d), "secret")
}

func TestSecretNodeModulesPackageLevel(t *testing.T) {
	d := t.TempDir()
	nm := filepath.Join(d, "node_modules")
	write(t, filepath.Join(nm, "pkg", ".env"), "x")
	wantWhy(t, Secret(nm), "") // package directories are skipped whole, by design
	write(t, filepath.Join(nm, ".env"), "x")
	wantWhy(t, Secret(nm), "secret") // above the package level
}
