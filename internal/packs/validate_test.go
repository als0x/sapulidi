package packsload

import "testing"

const projectRule = "category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"project\"\n"

func TestMarkerMayBeGitButNameMayNot(t *testing.T) {
	if _, errs := loadOne(t, "p", rule(projectRule+"names = [\"xyz-dir\"]\nmarkers = [\".git\"]\n")); len(errs) != 0 {
		t.Fatalf("marker .git rejected: %v", errs)
	}
	_, errs := loadOne(t, "p", rule(projectRule+"names = [\".git\"]\nmarkers = [\"pom.xml\"]\n"))
	wantErr(t, errs, "field names")
}

func TestGitLookAlikesRefused(t *testing.T) {
	for _, m := range []string{".GIT", "[.]git", ".git*", ".gi*"} {
		_, errs := loadOne(t, "p", rule(projectRule+"names = [\"xyz-dir\"]\nmarkers = ["+quote(m)+"]\n"))
		wantErr(t, errs, "field markers")
	}
	for _, n := range []string{".GIT", "[.]git"} {
		_, errs := loadOne(t, "p", rule(projectRule+"names = ["+quote(n)+"]\nmarkers = [\"pom.xml\"]\n"))
		wantErr(t, errs, "field names")
	}
}

func TestMarkerStillChecked(t *testing.T) {
	for _, m := range []string{"*", "a/b", "..", "s*", "~abc", ""} {
		_, errs := loadOne(t, "p", rule(projectRule+"names = [\"xyz-dir\"]\nmarkers = ["+quote(m)+"]\n"))
		wantErr(t, errs, "field markers")
	}
}
