package packsload

import (
	"os"
	"testing"
)

const optionalRule = "category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"temp\"\nnames = [\"abc*\"]\n"

func TestLoadReadsRecommend(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       bool
	}{
		{"true", "recommend = true\n", true},
		{"false", "recommend = false\n", false},
		{"absent", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, errs := loadOne(t, "p", rule(optionalRule+tc.line))
			if len(errs) != 0 || len(rules) != 1 {
				t.Fatalf("rules %v errs %v", rules, errs)
			}
			if rules[0].Recommend != tc.want {
				t.Fatalf("Recommend = %v, want %v", rules[0].Recommend, tc.want)
			}
		})
	}
}

func TestRecommendOnlyOnOptionalRules(t *testing.T) {
	buckets := map[string]string{
		"auto":  "category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"abc*\"]\n",
		"never": "category = \"pk-models\"\nbucket = \"never\"\nkind = \"path\"\nlocation = \"pk-location\"\n",
	}
	for bucket, body := range buckets {
		// The key itself is refused, whatever its value.
		for _, value := range []string{"true", "false"} {
			t.Run(bucket+" "+value, func(t *testing.T) {
				rules, errs := loadOne(t, "p", rule(body+"recommend = "+value+"\n"))
				if len(rules) != 0 {
					t.Fatalf("pack loaded: %v", rules)
				}
				wantErr(t, errs, "pack p", "rule r", "field recommend", "optional")
			})
		}
	}
}

func TestValidateRefusesRecommendOutsideOptional(t *testing.T) {
	rules, errs := loadOne(t, "go", validPack)
	if len(errs) != 0 || len(rules) != 1 {
		t.Fatalf("rules %v errs %v", rules, errs)
	}
	r := rules[0]
	r.Recommend = true
	err := Validate(r)
	if err == nil {
		t.Fatal("Validate accepted a recommended auto rule")
	}
	wantErr(t, []error{err}, "pack go", "rule go-build", "field recommend")
}

func TestUserPackCannotRecommend(t *testing.T) {
	// The key itself is refused, whatever its value.
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeUser(t, dir, "p.toml", rule(optionalRule+"recommend = "+value+"\n"))
			rules, errs := load(nil, dir, "linux")
			if len(rules) != 0 {
				t.Fatalf("pack loaded: %v", rules)
			}
			wantErr(t, errs, "pack p", "rule r", "field recommend", "only built-in packs can recommend")
		})
	}
}

func TestRecommendMustBeBoolean(t *testing.T) {
	rules, errs := loadOne(t, "p", rule(optionalRule+"recommend = \"true\"\n"))
	if len(rules) != 0 {
		t.Fatalf("pack loaded: %v", rules)
	}
	wantErr(t, errs, "pack p", "recommend", "incompatible types")
}
