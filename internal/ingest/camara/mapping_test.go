package camara

import (
	"os"
	"testing"
)

func loadXML(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func fixture(t *testing.T, name string) VotacionXML {
	t.Helper()
	var x VotacionXML
	if err := xmlUnmarshal(loadXML(t, name), &x); err != nil {
		t.Fatal(err)
	}
	return x
}

// 87460 (2026-01-21): the canonical detail hit.
func TestParseVotation_87460(t *testing.T) {
	x := fixture(t, "votacion_87460.xml")
	d, err := ParseVotation(x)
	if err != nil {
		t.Fatal(err)
	}
	if d.ExternalID != 87460 || d.VoteDate != "2026-01-21" || d.Boletin != "18036-05" {
		t.Fatalf("id=%d vote_date=%s boletin=%q", d.ExternalID, d.VoteDate, d.Boletin)
	}
	if len(d.Votes) == 0 {
		t.Fatal("zero votes decoded — XML contract drift")
	}
	if d.Totals.Yes == 0 || d.Totals.No == 0 {
		t.Fatalf("totals not decoded: %+v", d.Totals)
	}
	if d.Session.ID == 0 {
		t.Fatal("session linkage missing")
	}
	wanted := map[string]Vote{"En Contra": VoteNo, "Afirmativo": VoteYes}
	for _, v := range d.Votes {
		if wanted[v.RawText] != "" && v.Vote != wanted[v.RawText] {
			t.Fatalf("vote %q normalized to %q", v.RawText, v.Vote)
		}
	}
}

// 87461: the pareo shape — separate <Pareos> collection; every member also
// carries an explicit `No Vota` row.
func TestParseVotation_87461Pareos(t *testing.T) {
	x := fixture(t, "votacion_87461.xml")
	d, err := ParseVotation(x)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Pareos) == 0 {
		t.Fatal("pareos not decoded")
	}
	nNoVota := 0
	for _, v := range d.Votes {
		if v.Vote == VoteAbsent {
			nNoVota++
		}
	}
	if nNoVota < len(d.Pareos)*2 {
		t.Fatalf("expected pareo members to overlap `No Vota` rows: pareos=%d noVota=%d", len(d.Pareos), nNoVota)
	}
}

// Miss: `<Votacion xsi:nil="true"/>`.
func TestMissDetection(t *testing.T) {
	x := fixture(t, "votacion_miss.xml")
	if !x.Miss() {
		t.Fatal("miss body not detected")
	}
}

func TestNormalizeVoteOption(t *testing.T) {
	cases := map[string]Vote{
		"Afirmativo": VoteYes, "EN CONTRA": VoteNo, "Abstencion": VoteAbstain,
		"abstención": VoteAbstain, "No Vota": VoteAbsent, "Dispensado": VoteDispensed,
		"Pareo": VotePaired, "SEÑOR DIPUTADO": VoteOther, "": VoteOther,
	}
	for in, want := range cases {
		if got := NormalizeVoteOption(in); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestNormalizeBoletin(t *testing.T) {
	cases := map[string]string{"18036-05": "18036-05", " 18036-05 ": "18036-05", "": "", "S/N": "", "18036": "", "18036-": ""}
	for in, want := range cases {
		if got := NormalizeBoletin(in); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

// The one live-verified badly-formed API date (`2030-10T23:59:59` shape must
// not fail ingest — vote_date downgrades to the sortable sentinel instead).
func TestVoteDate_Sloppy(t *testing.T) {
	if got := VoteDate("2030-10T23:59:59"); got != badDate {
		t.Fatalf("sloppy date -> %q", got)
	}
	if got := VoteDate(""); got != badDate {
		t.Fatalf("empty -> %q", got)
	}
	if got := VoteDate("2024-11-14T20:03:49"); got != "2024-11-14" {
		t.Fatalf("clean -> %q", got)
	}
}

// The two deputy endpoints: all-history records may lack Fecha_Nacimiento.
func TestDeputies(t *testing.T) {
	vig, err := ParseDeputies(loadXML(t, "diputados_vigentes.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vig) < 100 {
		t.Fatalf("vigentes unexpectedly small: %d", len(vig))
	}
	all, err := ParseDeputies(loadXML(t, "diputados.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) <= len(vig) {
		t.Fatalf("history shorter than vigentes: %d <= %d", len(all), len(vig))
	}
}
