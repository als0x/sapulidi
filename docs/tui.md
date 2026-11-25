# The TUI

Run `sapulidi` with no arguments in a terminal. It scans, then opens on a list of **places**: the folders where it
found something (a project, a toolchain folder), with the leftovers of coding agents under the agent's name. The TUI
is for a person in a terminal; an agent uses `plan` and `report`. Every deletion still goes through the engine and its
safety checks.

## The four labels

| Label | Meaning |
|---|---|
| safe | the schedule removes it once you consented |
| recommended | Sapu Lidi would remove it; the schedule does so only if you allowed recommended rows in `setup` |
| your call | listed only: never preselected, never removed by the schedule. Every row of a category you switched off is one |
| protected | not removable now: a guard keeps it (the reason is shown with `g`) or you excluded it |

**At start every safe and recommended row is already selected.** Look through the selection, then press `d`; nothing is
removed before you confirm, and the confirmation lists every selected row, including the ones in places you have not
opened.

## Reading the screen

The first line of the summary says how much is safe, how much is recommended, how much is your call and how many rows
are protected. In a narrow window it reads `rec.` and `kept` for recommended and protected. The summary also shows the
amount to remove, the excluded count, the schedule and the free disk space. When a newer release is known from the
last update check, one more line says how to update; the TUI itself never uses the network.

| Column | Meaning |
|---|---|
| safe, recommended, your call | the size of the rows of the place with that label (`rec.` under 100 columns) |
| protected | the number of rows a guard protects (`prot.` under 100 columns) |
| updatedAt | the newest change among the rows of the place |

Rows of agent rules are grouped by agent, not by folder: `Claude Code · scratch`, `transcripts`, `versions` and
`plugin cache`, and `Codex · sessions` and `tmp`.

Markers: `[ ]` nothing selected, `[x]` everything you can select in the place is selected, `[~]` some of it,
`[-]` nothing in the place can be selected.

`enter` opens a place and lists its rows with size, age, updatedAt and label.

## Keys

| Key | Does |
|---|---|
| `up` `down` | move |
| `enter` | open a place, or a row's detail |
| `space` | select or clear. On a place: its safe and recommended rows. A place with only your call rows asks `y`/`n` first. In a mixed place `space` leaves the your call rows, so its box stays `[~]`; open it with `enter` and pick them there |
| `a` | select the safe and recommended rows in view, never a your call one. It only selects: to deselect, use `space` |
| `d` | delete the selection; the confirmation scrolls with `up` `down`, `j` `k`, `PgUp` `PgDn` |
| `x` | exclude the path for good (the same as `sapulidi exclude`) |
| `c` | switch between the places and the flat list |
| `o` | show the rows of switched off categories in the flat list (the places always list them) |
| `g` | show what is protected, with the reason; on the places it also lists places that hold only protected rows |
| `u` | read-only list of big folders no rule covers |
| `/` `s` | search, sort |
| `?` | help screen: labels, markers and keys |
| `esc` `q` | back, quit |

## Selection

The selection belongs to the whole scan, not to the screen. Switching views, `o`, `g`, search and sort never change
it, and the confirmation lists every selected row, including the ones the current view hides.

## Folders no rule covers

`u` lists the big folders that no rule covers, found with the same search as `sapulidi report`. It runs after the
places are shown: the summary says `searching` until it ends, `esc` on the places stops it, and a search that timed
out or was cut short shows `partial` with sizes ending in `+`. Nothing there can be selected or deleted.
