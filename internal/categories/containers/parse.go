package containers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dfRow is one line of `<engine> system df --format json`. Podman prints an array with Raw* byte counts,
// Docker one object per line with human sizes ("1.5GB", "1GB (66%)") only.
type dfRow struct {
	Type           string
	RawSize        *int64
	RawReclaimable *int64
	Size           string
	Reclaimable    string
}

type usage struct {
	label             string
	size, reclaimable int64
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([kKMGTP]?)B`)

var units = map[string]float64{"": 1, "k": 1e3, "K": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "P": 1e15}

// parseSize reads a docker style size; the text after the unit ("(66%)") is ignored.
func parseSize(s string) (int64, bool) {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return int64(f*units[m[2]] + 0.5), true
}

var dfLabels = map[string]string{
	"Images": "images", "Containers": "containers", "Local Volumes": "volumes", "Build Cache": "build cache",
}

// decodeList reads a JSON array or one JSON object per line.
func decodeList[T any](out []byte) ([]T, error) {
	out = bytes.TrimSpace(out)
	var res []T
	if len(out) == 0 {
		return nil, nil
	}
	if out[0] == '[' {
		return res, json.Unmarshal(out, &res)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		res = append(res, v)
	}
	return res, nil
}

// parseDf returns the usage rows it knows. Anything unexpected is an error, which the caller treats as "engine
// unavailable".
func parseDf(out []byte) ([]usage, error) {
	rows, err := decodeList[dfRow](out)
	if err != nil {
		return nil, err
	}
	var res []usage
	for _, r := range rows {
		label, ok := dfLabels[r.Type]
		if !ok {
			continue
		}
		u := usage{label: label}
		if r.RawSize != nil {
			u.size = *r.RawSize
		} else if u.size, ok = parseSize(r.Size); !ok {
			return nil, fmt.Errorf("unreadable size %q", r.Size)
		}
		if r.RawReclaimable != nil {
			u.reclaimable = *r.RawReclaimable
		} else if n, ok := parseSize(r.Reclaimable); ok {
			u.reclaimable = n
		}
		res = append(res, u)
	}
	if len(res) == 0 {
		return nil, errors.New("no usage rows")
	}
	return res, nil
}

var (
	idRe   = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// ctr is one entry of `<engine> ps -a --format json`. Podman: Id, array Mounts and Ports, epoch seconds for
// ExitedAt, StartedAt and Created. Docker: ID, string Mounts and Ports, CreatedAt as text, no exit time.
type ctr struct {
	ID        string `json:"Id"`
	State     string
	ExitedAt  int64
	StartedAt int64
	Created   json.RawMessage
	CreatedAt string
	Mounts    json.RawMessage
	Ports     json.RawMessage
}

// mountsEmpty is true only when the engine said there are none: an empty array or an empty string. A missing
// field, null, or any other shape is unknown, and unknown is never removable.
func (c ctr) mountsEmpty() bool {
	s := strings.TrimSpace(string(c.Mounts))
	return s == "[]" || s == `""`
}

func (c ctr) createdTime() (time.Time, bool) {
	var n int64
	if json.Unmarshal(c.Created, &n) == nil && n > 0 {
		return secs(n), true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05 -0700 MST"} {
		if t, err := time.Parse(layout, c.CreatedAt); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseContainers(out []byte) ([]ctr, error) { return decodeList[ctr](out) }

type podmanPort struct {
	HostPort int `json:"host_port"`
	Range    int `json:"range"`
}

const maxPorts = 4096

var (
	dockerPortRe  = regexp.MustCompile(`:([0-9]+)(?:-([0-9]+))?->`)
	unpublishedRe = regexp.MustCompile(`^[0-9]+(?:-[0-9]+)?/[a-z]+$`)
)

func portRange(lo, hi int) ([]int, bool) {
	if lo <= 0 || hi < lo || hi-lo >= maxPorts {
		return nil, false
	}
	var res []int
	for p := lo; p <= hi; p++ {
		res = append(res, p)
	}
	return res, true
}

// hostPorts lists the published host ports of a container for both engines, ranges expanded. ok is false when
// a Ports value is there but cannot be read: the caller must then treat the answer as unknown.
func (c ctr) hostPorts() ([]int, bool) {
	switch strings.TrimSpace(string(c.Ports)) {
	case "", "null", "[]", `""`:
		return nil, true
	}
	var res []int
	var ps []podmanPort
	if json.Unmarshal(c.Ports, &ps) == nil {
		for _, p := range ps {
			if p.HostPort == 0 {
				continue
			}
			n := max(p.Range, 1)
			got, ok := portRange(p.HostPort, p.HostPort+n-1)
			if !ok {
				return nil, false
			}
			res = append(res, got...)
		}
		return res, len(res) <= maxPorts
	}
	var s string
	if json.Unmarshal(c.Ports, &s) != nil {
		return nil, false
	}
	for e := range strings.SplitSeq(s, ",") {
		e = strings.TrimSpace(e)
		switch {
		case e == "":
		case strings.Contains(e, "->"):
			m := dockerPortRe.FindStringSubmatch(e)
			if m == nil {
				return nil, false
			}
			lo, _ := strconv.Atoi(m[1])
			hi := lo
			if m[2] != "" {
				hi, _ = strconv.Atoi(m[2])
			}
			got, ok := portRange(lo, hi)
			if !ok {
				return nil, false
			}
			res = append(res, got...)
		case !unpublishedRe.MatchString(e):
			return nil, false
		}
	}
	return res, len(res) <= maxPorts
}

func secs(n int64) time.Time { return time.Unix(n, 0) }
