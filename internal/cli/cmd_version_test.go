package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/version"
)

func TestMainUnknownCommandExits2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"nonesuch"}, &out, &errb); code != 2 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "usage") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestHelpListsCommandsSorted(t *testing.T) {
	Register(Command{Name: "zzz", Summary: "last summary"})
	Register(Command{Name: "aaa", Summary: "first summary"})
	defer delete(commands, "zzz")
	defer delete(commands, "aaa")
	var out, errb bytes.Buffer
	if code := Main([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	s := out.String()
	idx := []int{}
	for _, w := range []string{"aaa", "help", "version", "zzz"} {
		i := strings.Index(s, w)
		if i < 0 {
			t.Fatalf("%q missing:\n%s", w, s)
		}
		idx = append(idx, i)
	}
	for i := 1; i < len(idx); i++ {
		if idx[i-1] > idx[i] {
			t.Fatalf("not sorted:\n%s", s)
		}
	}
	if !strings.Contains(s, "first summary") || !strings.Contains(s, "last summary") {
		t.Fatalf("summaries missing:\n%s", s)
	}
}

func TestVersionJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "version"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["version"] != version.Version {
		t.Fatalf("out %q err %v", out.String(), err)
	}
}

func TestJSONFlagAfterCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"version", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["version"] != version.Version {
		t.Fatalf("out %q err %v", out.String(), err)
	}
}

func TestVersionPrintsTheNoticeWithoutFetching(t *testing.T) {
	e := newUpdateEnv(t)
	n := api(t, 200, `{"tag_name":"v0.9.0"}`)
	e.cache(t, "v0.4.0", 72*time.Hour)
	writeFile(t, filepath.Join(e.state, "installed-by", "install.sh"), "")
	out, errs, code := output(t, "version")
	want := "0.3.0\nSapu Lidi v0.4.0 is out (you have 0.3.0). Update: sapulidi update\n"
	if code != 0 || out != want || *n != 0 || e.started != 0 {
		t.Fatalf("code %d requests %d started %d %q %q", code, *n, e.started, out, errs)
	}
}

func TestVersionNoNotice(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, *updateEnv){
		"not newer":        func(t *testing.T, e *updateEnv) { e.cache(t, "v0.3.0", time.Hour) },
		"no cache":         func(*testing.T, *updateEnv) {},
		"update.check off": func(t *testing.T, e *updateEnv) { e.config(t, "[update]\ncheck = false\n") },
		"no repository":    func(*testing.T, *updateEnv) { version.Repo = "" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newUpdateEnv(t)
			if name != "no cache" && name != "not newer" {
				e.cache(t, "v0.4.0", time.Hour)
			}
			setup(t, e)
			if out, errs, code := output(t, "version"); code != 0 || out != "0.3.0\n" {
				t.Fatalf("code %d %q %q", code, out, errs)
			}
		})
	}
}

func TestVersionJSONNamesTheRepo(t *testing.T) {
	newUpdateEnv(t)
	for _, repo := range []string{"als0x/sapulidi", ""} {
		version.Repo = repo
		out, _, code := output(t, "--json", "version")
		var v map[string]string
		if err := json.Unmarshal([]byte(out), &v); code != 0 || err != nil {
			t.Fatalf("code %d %q %v", code, out, err)
		}
		if got, ok := v["repo"]; !ok || got != repo {
			t.Fatalf("repo %q: %q", repo, out)
		}
	}
}

func TestVersionText(t *testing.T) {
	var out, errb bytes.Buffer
	Main([]string{"version"}, &out, &errb)
	if strings.TrimSpace(out.String()) != version.Version {
		t.Fatalf("out %q", out.String())
	}
}
