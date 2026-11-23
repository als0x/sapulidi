# Rule packs

A pack is a TOML file `packs/<pack>.toml` that tells sapulidi which artefacts of one ecosystem it may propose for
removal. Packs are data: no Go is needed to add one. Built-in packs are embedded in the binary at build time (they
reach users with a release). Users can add their own in `<config>/packs/*.toml`; those are validated exactly the same
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

## What the validator refuses

A pack with any invalid rule is ignored as a whole and the error (naming pack, rule id and field) is reported.

- `pack` differs from the file name, or is not lower-case letters, digits and `-`.
- `id` does not match `^[a-z0-9][a-z0-9-]{0,40}$`, or repeats inside the pack. `rev` is below 1.
- `category` is not a known category; `bucket` differs from the category's bucket (only `never` may differ);
  `kind` is not one of the kinds above.
- `names` is empty for `temp`, `project` and `file`. `markers` is empty for `project` and `file`.
- A name or marker pattern that is not a valid glob; is longer than 64 bytes; contains `/`, `..`, white space, a
  control character or a leading `~`; is `.`; has fewer than 3 literal characters or more than 3 wildcards (`*`,
  `?` and each `[...]` class count as one); or could match `.git`, `src`, `lib`, `app`, `docs`, `test` or `tests`
  in lower, upper or capitalised form (`S*`, `[S]rc`, `.GIT`), because macOS filesystems ignore case.
- `names` on a rule whose kind is not `temp`, `project` or `file`; `markers` outside `project` and `file`;
  `tool`/`action` outside `cache`; `strategy` outside `versions`; `location` outside `versions` and `path`.
- `min_age` below the bucket minimum: 1 h for `auto`, 24 h for `optional` (none for `never`). Without `min_age` the
  bucket minimum applies. Ages are Go durations or whole days (`30d`), at most 10 years.
- `tool`/`action` of a `cache` rule is not a known pair; `strategy` or `location` of a `versions` rule, or
  `location` of a `path` rule, is not known.
- `os` has a value other than `darwin` or `linux`; rules are only used on the OS they list.
- Any key not in the schema. Absolute paths, commands and scripts have no key, so they cannot be expressed.

User packs must be regular files of at most 1 MiB owned by the user directly in `<config>/packs/`, and that
directory must be owned by the user, not a symlink and not writable by group or others. Symlinks are ignored and a
user pack cannot reuse a built-in pack name.

## Adding a pack

1. Create `packs/<pack>.toml` as above, one rule per artefact, a marker next to every per-project artefact.
2. Add fixtures in `internal/categories/<category>/testdata/<pack>/`: a fake project tree with the expected
   candidates and the expected keeps (hand-made data, tracked files, secrets next to the marker).
3. When the pack adds or widens an auto rule, update `packs/testdata/autorules/<pack>.golden` and bump `rev` for a
   widened rule.
4. Run `bash tests/run.sh`.
