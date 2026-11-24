# The TUI

Run `sapulidi` with no arguments in a terminal. It scans, then opens on a list of **places**: the folders where it
found something (a project, a toolchain folder). The TUI is for a person in a terminal; an agent uses `plan` and
`report`. Every deletion still goes through the engine and its safety checks.

## Reading the screen

The first line of the summary answers the question: what is free now automatically, what you can free if you choose,
and how many rows are protected. The summary also shows the amount to remove, the excluded count, the schedule and
the free disk space.

| Column | Meaning |
|---|---|
| ready | removable now: the rules allow it and you have consented |
| optional | rules that are switched off: listed, never preselected |
| kept | rows a guard protects, with the reason (shown with `g`) |
| updatedAt | the newest change among the rows of the place |

Markers: `[ ]` nothing selected, `[x]` everything you can select in the place is selected, `[~]` some of it,
`[-]` nothing in the place can be selected.

`enter` opens a place and lists its rows with size, age, updatedAt and source. Optional rows are always listed there.

## Keys

| Key | Does |
|---|---|
| `up` `down` | move |
| `enter` | open a place, or a row's detail |
| `space` | select. On a place: every ready row in it, never the optional ones. A place with only optional rows asks `y`/`n` first. In a place with both, `space` takes the ready rows only, so its box stays `[~]`; open it with `enter` and pick the optional rows there |
| `a` | select all ready rows (in the flat list: every row in view) |
| `d` | delete the selection; the confirmation scrolls with `up` `down`, `j` `k`, `PgUp` `PgDn` |
| `x` | exclude the path for good (the same as `sapulidi exclude`) |
| `c` | switch between the places and the flat list |
| `o` | show optional rows in the flat list (the places always list them) |
| `g` | show what a guard keeps, with the reason; on the places it also lists places that hold only kept rows |
| `u` | read-only list of big folders no rule covers |
| `/` `s` | search, sort |
| `?` | help screen: columns, markers and keys |
| `esc` `q` | back, quit |

## Selection

The selection belongs to the whole scan, not to the screen. Switching views, `o`, `g`, search and sort never change
it, and the confirmation lists every selected row, including the ones the current view hides.

## Folders no rule covers

`u` lists the big folders that no rule covers, found with the same search as `sapulidi report`. It runs after the
places are shown: the summary says `searching` until it ends, `esc` on the places stops it, and a search that timed
out or was cut short shows `partial` with sizes ending in `+`. Nothing there can be selected or deleted.
