// Package update checks for new iCode releases on GitHub.
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository hosting iCode releases.
const Repo = "ponygates/icode"

// Info describes the result of a release check.
type Info struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	HTMLURL     string `json:"html_url"`
	ReleaseDate string `json:"release_date,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// Check queries the GitHub latest-release API and compares it with the local
// version. current/latest are compared without a leading "v". An empty result
// with nil error means GitHub was unreachable and the check should be skipped.
func Check(current string) (*Info, error) {
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "iCode-updater/"+current)

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: status %d", resp.StatusCode)
	}

	var rel struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	cur := strings.TrimPrefix(current, "v")
	info := &Info{
		Current:     strings.TrimPrefix(current, "v"),
		Latest:      latest,
		HTMLURL:     rel.HTMLURL,
		ReleaseDate: rel.PublishedAt,
		Notes:       rel.Body,
	}
	if latest != "" && latest != cur {
		info.Available = versionGreater(latest, cur)
	}
	return info, nil
}

// versionGreater reports whether a is semantically newer than b (x.y.z).
func versionGreater(a, b string) bool {
	as := parseVersion(a)
	bs := parseVersion(b)
	for i := 0; i < 3; i++ {
		if as[i] > bs[i] {
			return true
		}
		if as[i] < bs[i] {
			return false
		}
	}
	return false
}

func parseVersion(s string) [3]int {
	var out [3]int
	parts := strings.SplitN(s, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		if n, err := strconv.Atoi(parts[i]); err == nil {
			out[i] = n
		}
	}
	return out
}
