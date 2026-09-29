// Command draft_affiliations produces a FIRST-DRAFT data/affiliations.json
// by scraping each current deputy's ficha parlamentaria for "Partido:" and
// mapping the full name to parties.json short_name. The output is a bot
// draft — a human reviews and edits before it is applied via
// `go run ./cmd/sync -seed <affiliations.json>` (docs/DESIGN.md decisions).
//
// Run: go run ./scripts/draft_affiliations.go
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"congress-visualizer/internal/ingest/camara"
)

type Party struct {
	ShortName string `json:"short_name"`
	Name      string `json:"name"`
	Color     string `json:"color"`
}
type Affiliation struct {
	Chamber    string  `json:"chamber"`
	ExternalID string  `json:"external_id"`
	Party      string  `json:"party"`
	Start      string  `json:"start"`
	End        *string `json:"end"`
	Source     string  `json:"source"` // provenance for the human review
}

var (
	partidoRE = regexp.MustCompile(`(?i)Partido:\s*([^<\n]+?)<`)
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	// deputies (live list from the open-data API)
	client := camara.NewClient("", 150*time.Millisecond)
	deps, err := client.DiputadosVigentes(ctx)
	if err != nil {
		log.Error("vigentes", "err", err)
		os.Exit(1)
	}

	// parties.json maps full name -> short_name
	partiesBody, err := os.ReadFile("data/parties.json")
	if err != nil {
		log.Error("read parties", "err", err)
		os.Exit(1)
	}
	var parties []Party
	if err := json.Unmarshal(partiesBody, &parties); err != nil {
		log.Error("parse parties", "err", err)
		os.Exit(1)
	}
	fullToShort := map[string]string{}
	for _, p := range parties {
		fullToShort[strings.ToLower(p.Name)] = p.ShortName
	}

	hc := &http.Client{Timeout: 30 * time.Second}
	var affiliations []Affiliation
	rows := [][]string{{"external_id", "name", "party_full", "party_short", "start", "source_url"}}
	nMissing := 0
	for i, d := range deps {
		if i > 0 && i%20 == 0 {
			log.Info("progress", "fetched", i, "of", len(deps))
		}
		url := fmt.Sprintf("https://www.camara.cl/diputados/detalle/ficha_parlamentaria.aspx?prmId=%d", d.DIPID)
		partyFull := ""
		if body, err := fetch(hc, url); err == nil {
			var bodyBytes []byte = []byte(body)
			if m := partidoRE.FindSubmatch(bodyBytes); m != nil {
				partyFull = strings.TrimSpace(htmlUnescape(string(m[1])))
			}
		} else {
			log.Warn("ficha fetch", "id", d.DIPID, "err", err)
		}
		short := fullToShort[strings.ToLower(strings.TrimSpace(partyFull))]
		if short == "" && partyFull != "" {
			// unmapped full name: emit verbatim, flagged for the human
			short = "??:" + partyFull
		}
		rows = append(rows, []string{fmt.Sprint(d.DIPID),
			displayName(d), partyFull, short, "2026-03-11", url})
		affiliations = append(affiliations, Affiliation{
			Chamber: "camara", ExternalID: fmt.Sprint(d.DIPID),
			Party: short, Start: "2026-03-11",
		})
		time.Sleep(300 * time.Millisecond)
	}

	// annotations for the human reviewer
	type draftDoc struct {
		Note         string        `json:"_note"`
		GeneratedAt  string        `json:"_generated_at"`
		UnmappedRaw  []string      `json:"_unmapped_party_names,omitempty"`
		Affiliations []Affiliation `json:"affiliations"`
	}
	doc := draftDoc{
		Note: "BOT DRAFT - review before applying: `go run ./cmd/sync -seed traces/affiliations.draft.json`",
		GeneratedAt: time.Now().Format(time.RFC3339),
		Affiliations: affiliations,
	}
	body, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile("traces/affiliations.draft.json", body, 0o644); err != nil {
		log.Error("write draft", "err", err)
		os.Exit(1)
	}
	// CSV review sheet alongside (one row per deputy, visual diff surface)
	f, err := os.Create("traces/affiliations.review.ficha.csv")
	if err != nil {
		log.Error("write review", "err", err)
		os.Exit(1)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.WriteAll(rows)
	w.Flush()
	log.Info("draft written", "deputies", len(deps), "unmapped", nMissing, "out", "traces/affiliations.draft.json")
}

func fetch(hc *http.Client, url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept-Language", "es-CL,es;q=0.9")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(body), err
}

func displayName(d camara.DiputadoXML) string {
	return strings.Join(nonEmpty(d.Nombre, d.ApellidoPaterno, d.ApellidoMaterno), " ")
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func htmlUnescape(s string) string {
	return parteReplacer.Replace(reStripHTMLTags.ReplaceAllString(s, " "))
}

var reStripHTMLTags = regexp.MustCompile(`<[^>]+>`)

var parteReplacer = strings.NewReplacer(
	"&aacute;", "á", "&eacute;", "é", "&iacute;", "í", "&oacute;", "ó",
	"&uacute;", "ú", "&ntilde;", "ñ",
)
