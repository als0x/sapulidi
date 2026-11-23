# Rule packs

A pack is a TOML file `packs/<pack>.toml` that tells sapulidi which artefacts of one ecosystem it may propose for
removal. Packs are data: no Go is needed to add one. Built-in packs are embedded in the binary at build time and
reach users with a release. You can add your own in `<config>/packs/*.toml`; they are validated exactly the same
way and their rules are named `user-<name>/...`.

## Format

```toml
pack = "go"            # must equal the file name
[[rule]]
id = "go-build"
rev = 1
category = "temp"
bucket = "auto"        # must equal the category's bucket, except "never" may lower any rule to report-only
kind = "temp"
names = ["go-build*"]
min_age = "1h"
# markers = ["go.mod"]        kind = "project" and "file"
# tool = "go" action = "clean-cache"   kind = "cache"
# strategy = "keep-active-and-previous" location = "nvm-versions"   kind = "versions"
# location = "..."            kind = "path"
# os = ["darwin"]
```

Kinds: `temp` (entries directly in the temp dir), `project` (directories inside roots next to a marker), `file`
(files inside roots), `cache` (a global cache cleaned by a known tool command), `versions` (version directories
filtered by a strategy written in Go), `path` (a known per-user location handled by its category).

Ages are Go durations or whole days (`30d`), at most 10 years. The minimum is 1 h for `auto` and 24 h for
`optional`; without `min_age` the bucket minimum applies.

## What the validator refuses

A pack with any invalid rule is ignored as a whole, and the error names the pack, the rule id and the field.

- `pack` differs from the file name, or is not lower-case letters, digits and `-`.
- `id` does not match `^[a-z0-9][a-z0-9-]{0,40}$` or repeats inside the pack; `rev` is below 1.
- `category` is unknown; `bucket` differs from the category's bucket (only `never` may differ); `kind` is unknown.
- `names` is empty for `temp`, `project` and `file`; `markers` is empty for `project` and `file`.
- A name or marker pattern that is not a valid glob; is longer than 64 bytes; contains `/`, `..`, white space, a
  control character or a leading `~`; has fewer than 3 literal characters or more than 3 wildcards; or could match
  `.git`, `src`, `lib`, `app`, `docs`, `test` or `tests` in any letter case.
- A key that does not belong to the rule's kind (`names`, `markers`, `tool`/`action`, `strategy`, `location`).
- `os` has a value other than `darwin` or `linux`.
- Any key not in the schema. Absolute paths, commands and scripts have no key, so they cannot be expressed.

User packs must be regular files of at most 1 MiB owned by you, directly in `<config>/packs/`, in a directory that
is owned by you, not a symlink and not writable by group or others. A user pack cannot reuse a built-in pack name.
No pack can enable anything in the never bucket or the deny list: those are code.

## End-to-end example: a pack for a new ecosystem

Say you want sapulidi to propose `.elm-stuff` folders next to an `elm.json`.

1. Write `packs/elm.toml`:

   ```toml
   pack = "elm"

   [[rule]]
   id = "elm-stuff"
   rev = 1
   category = "build-output"
   bucket = "optional"
   kind = "project"
   names = [".elm-stuff"]
   markers = ["elm.json"]
   min_age = "30d"
   ```

   The marker means a `.elm-stuff` directory is considered only when `elm.json` sits next to it. `optional` keeps
   it off until the user enables the category.

2. Add fixtures under `internal/categories/build-output/testdata/elm/`: a project with `elm.json` and
   `.elm-stuff` (expected candidate), and one with `.elm-stuff` but no `elm.json` (expected keep).
3. An `optional` rule needs no golden file. For an `auto` rule, update `packs/testdata/autorules/elm.golden`, and
   bump `rev` whenever you widen the rule.
4. Run `bash tests/run.sh`. The embedded-pack check loads every pack and fails on any validator error.
5. To use it yourself without a release, copy it to `<config>/packs/elm.toml`; the rule is then named
   `user-elm/elm-stuff`. Run `sapulidi plan` to see what it would remove.
