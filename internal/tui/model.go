// Package tui holds the terminal UI as pure functions: Update turns a Model and a message into a new Model and a
// list of effects, View turns a Model and a size into lines. Nothing here touches a terminal, the disk or the
// clock; the terminal layer is internal/tui/term and the wiring is elsewhere.
package tui

import "time"

// Screen is the page the model is on.
type Screen int

// The screens.
const (
	ScreenScanning Screen = iota
	ScreenList            // the flat candidate list
	ScreenDetail
	ScreenConfirm
	ScreenResult
	ScreenPlaces // the start screen after the scan: one row per place
	ScreenPlace  // the rows of Model.Place
)

// SortKey is the list order.
type SortKey int

// The sort keys, in the order `s` cycles through them.
const (
	SortSize    SortKey = iota // largest first
	SortAge                    // oldest first (unknown last)
	SortUpdated                // stalest first (unknown last)
	SortPath
)

// Names of the special keys in KeyMsg.Special.
const (
	KeyUp        = "up"
	KeyDown      = "down"
	KeyEnter     = "enter"
	KeyEsc       = "esc"
	KeyBackspace = "backspace"
	KeyPgUp      = "pgup"
	KeyPgDn      = "pgdn"
)

// Msg is an input to Update.
type Msg interface{ isMsg() }

// KeyMsg is one key press. A printable key sets Rune; a named key sets Special; Ctrl is set with the letter in Rune.
type KeyMsg struct {
	Rune    rune
	Special string
	Ctrl    bool
}

// ResizeMsg reports a new window size.
type ResizeMsg struct{ Width, Height int }

// ScanProgressMsg reports that a category has checked Done of Total items.
type ScanProgressMsg struct {
	Category    string
	Done, Total int
}

// ScanDoneMsg ends the scan with every row and the summary numbers.
type ScanDoneMsg struct {
	Rows    []Row
	Summary SummaryData
}

// DeleteDoneMsg ends a deletion.
type DeleteDoneMsg struct{ Result Result }

// ExclusionChangedMsg reports that an exclusion was stored (Excluded) or removed for Path.
type ExclusionChangedMsg struct {
	Path     string
	Excluded bool
}

func (KeyMsg) isMsg()              {}
func (ResizeMsg) isMsg()           {}
func (ScanProgressMsg) isMsg()     {}
func (ScanDoneMsg) isMsg()         {}
func (DeleteDoneMsg) isMsg()       {}
func (ExclusionChangedMsg) isMsg() {}

// Effect is something the caller must do after Update.
type Effect interface{ isEffect() }

// StartScan asks for the scan to begin.
type StartScan struct{}

// CancelScan asks for the running scan to stop.
type CancelScan struct{}

// Delete asks for exactly Paths to be removed, in this order.
type Delete struct{ Paths []string }

// AddExclusion asks for Path to be stored as an exclusion.
type AddExclusion struct{ Path string }

// RemoveExclusion asks for the exclusion of Path to be removed.
type RemoveExclusion struct{ Path string }

// Quit asks the program to exit.
type Quit struct{}

func (StartScan) isEffect()       {}
func (CancelScan) isEffect()      {}
func (Delete) isEffect()          {}
func (AddExclusion) isEffect()    {}
func (RemoveExclusion) isEffect() {}
func (Quit) isEffect()            {}

// SummaryData is what the summary panel shows besides what the rows say.
type SummaryData struct {
	Now      time.Time     // the moment of the scan; ages are measured from it
	Agents   int           // agent tools the plugin is installed into
	Interval time.Duration // schedule interval; 0 = no job installed
	Next     time.Time     // next scheduled run; zero = unknown
	FreeNow  uint64        // free disk bytes now; 0 = unknown
	Paused   bool          // the pause file exists
	Roots    []string      // project roots of the scan; a row under one belongs to the longest match
	Home     string        // the home directory, shown as ~ in places
}

// Result is what the engine reported for a deletion.
type Result struct {
	Freed   int64
	Removed []string // paths that were removed
	Kept    []KeptItem
	Err     string
	Pending bool // set by the model while the deletion runs
}

// KeptItem is a path a guard kept at the moment of removal.
type KeptItem struct{ Path, Why string }

// scanLine is one category in the progress view.
type scanLine struct {
	Name        string
	Done, Total int
}

// Model is the whole UI state. Treat it as immutable: Update returns a changed copy and never edits its input.
type Model struct {
	Plain        bool
	Screen       Screen
	Rows         []Row
	Summary      SummaryData
	Selected     map[string]bool
	Cursor       int // index into the visible rows
	ShowOptional bool
	Pinned       string // a row that stays visible although the views hide it, until the cursor leaves it
	ShowKept     bool   // `g`: rows kept by a guard are listed (dim, never selectable)
	Sort         SortKey
	Filter       string
	Filtering    bool
	Place        string // the open place (ScreenPlace, and the detail or confirmation opened from it)
	PlacesFilter string // the places search, kept while a place is open
	Back         Screen // the list screen that detail, confirmation and result return to
	Status       string
	Scan         []scanLine
	Confirm      []string // the paths the confirmation was opened for; `y` revalidates them
	ConfirmTop   int      // first confirmation entry shown (the list scrolls)
	Result       Result
	W, H         int // window size from ResizeMsg; 0 = not reported yet (taken as large enough)
}

// NewModel is the model before the scan starts.
func NewModel(plain bool) Model {
	return Model{Plain: plain, Screen: ScreenScanning, Selected: map[string]bool{}}
}

// Init returns the effects to run at start.
func Init() []Effect { return []Effect{StartScan{}} }
