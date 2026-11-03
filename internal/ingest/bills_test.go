package ingest

import (
	"context"
	"testing"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/ingest/camara"
)

// Bill-title regression tests (docs/DESIGN.md gap 2): "the non-empty
// Articulo of the MOST RECENT votation ingested for that bill (deterministic
// across ingest orders; bills.title_date tracks it), else placeholder; a
// manual/enriched source is never clobbered." The rule is enforced inside
// the ingest transaction by applyBillTitle (re-reading the existing row) —
// these tests pin it at the real ingest path, s.upsertVotation.

func vd(ext int, boletin, subject, voteDate string) camara.VotationData {
	return camara.VotationData{
		ExternalID: ext, Boletin: boletin, Subject: subject, VoteDate: voteDate,
		Date: voteDate + "T14:22:00",
	}
}

func billRow(t *testing.T, s *Syncer, boletin string) database.Bill {
	t.Helper()
	b, err := s.q.GetBill(context.Background(), boletin)
	if err != nil {
		t.Fatalf("read bill %s: %v", boletin, err)
	}
	return b
}

func ingest(t *testing.T, s *Syncer, d camara.VotationData) {
	t.Helper()
	if o := s.upsertVotation(context.Background(), d); o.VotationID == 0 {
		t.Fatalf("ingest of votation %d failed: %+v", d.ExternalID, o)
	}
}

// A fresh bill records the votation's Articulo, provenance, and date.
func TestIngestRecordsVotationTitle(t *testing.T) {
	s, _ := newTestSyncer(t)

	ingest(t, s, vd(42300, "15351-07", "Asigna dress code", "2026-01-20"))

	b := billRow(t, s, "15351-07")
	if b.Title != "Asigna dress code" || b.TitleSource != titleSourceIngest || !b.TitleDate.Valid || b.TitleDate.String != "2026-01-20" {
		t.Fatalf("bill row = %+v", b)
	}
}

// No Articulo -> the nameplate. The row is a placeholder that any later
// Articulo must replace.
func TestIngestWithoutArticuloWritesNameplate(t *testing.T) {
	s, _ := newTestSyncer(t)

	ingest(t, s, vd(42301, "15351-07", "  ", "2026-02-10"))

	b := billRow(t, s, "15351-07")
	if b.Title != billNameplate("15351-07") || b.TitleSource != titleSourceIngest {
		t.Fatalf("nameplate row = %+v", b)
	}
}

// Catch-the-old-bug (down-sweep case): votations arrive newest-first and
// older ones keep coming for the same bill afterwards. In either order the
// stored title is the one from the most recent votation — the old
// unconditional UpsertBillStub titled each bill by its OLDEST Articulo when
// the sweep ran new->old and downgraded on any re-sync.
func TestIngestTitleIsOrderIndependent(t *testing.T) {
	for _, pair := range [][2]camara.VotationData{
		{vd(42310, "15351-07", "Title of the OLDER votation", "2026-01-10"),
			vd(42311, "15351-07", "Title of the NEWER votation", "2026-03-30")},
		{vd(42311, "15351-07", "Title of the NEWER votation", "2026-03-30"),
			vd(42310, "15351-07", "Title of the OLDER votation", "2026-01-10")},
	} {
		s, _ := newTestSyncer(t)
		for _, d := range pair {
			ingest(t, s, d)
		}
		b := billRow(t, s, "15351-07")
		if b.Title != "Title of the NEWER votation" || b.TitleDate.String != "2026-03-30" {
			t.Fatalf("order saw bill row = %+v — recency rule lost", b)
		}
	}
}

// Exact title-date ties settle by greater title, keeping the outcome
// independent of ingest order.
func TestIngestTitleTieIsOrderIndependent(t *testing.T) {
	for _, pair := range [][2]camara.VotationData{
		{vd(42320, "15351-07", "Alpha older wording", "2026-03-01"),
			vd(42321, "15351-07", "Beta later wording", "2026-03-01")},
		{vd(42321, "15351-07", "Beta later wording", "2026-03-01"),
			vd(42320, "15351-07", "Alpha older wording", "2026-03-01")},
	} {
		s, _ := newTestSyncer(t)
		for _, d := range pair {
			ingest(t, s, d)
		}
		if b := billRow(t, s, "15351-07"); b.Title != "Beta later wording" {
			t.Fatalf("tie fell to order: bill row = %+v", b)
		}
	}
}

// manual/enriched rows are protected in the ingest transaction: a votation
// (even newer) never clobbers them — only the enrichment job may upgrade.
func TestIngestDoesNotClobberManualTitle(t *testing.T) {
	for _, src := range []string{titleSourceManual, titleSourceEnriched} {
		s, db := newTestSyncer(t)
		if _, err := db.Exec(`INSERT INTO bills (id, title, title_source)
			VALUES ('15351-07', 'Phase 2 enrichment title', ?)`, src); err != nil {
			t.Fatalf("seed %s bill: %v", src, err)
		}

		ingest(t, s, vd(42330, "15351-07", "Votation title, newer", "2026-05-05"))

		b := billRow(t, s, "15351-07")
		if b.Title != "Phase 2 enrichment title" || b.TitleSource != src {
			t.Fatalf("%s title clobbered: bill row = %+v", src, b)
		}
	}
}

// The nameplate never replaces a recorded Articulo — even one from an older
// votation — and an Articulo always replaces a nameplate-only row.
func TestIngestNameplateVersusArticulo(t *testing.T) {
	t.Run("nameplate does not replace recorded title", func(t *testing.T) {
		s, _ := newTestSyncer(t)
		ingest(t, s, vd(42340, "15351-07", "Real Articulo title", "2026-02-02"))
		ingest(t, s, vd(42341, "15351-07", "", "2026-06-06")) // newer, but no Articulo

		b := billRow(t, s, "15351-07")
		if b.Title != "Real Articulo title" || b.TitleDate.String != "2026-02-02" {
			t.Fatalf("nameplate replaced an Articulo title: bill row = %+v", b)
		}
	})
	t.Run("Articulo replaces nameplate-only row", func(t *testing.T) {
		s, _ := newTestSyncer(t)
		ingest(t, s, vd(42342, "15351-07", "", "2026-06-06"))
		ingest(t, s, vd(42343, "15351-07", "Real Articulo title", "2026-02-02")) // older

		b := billRow(t, s, "15351-07")
		if b.Title != "Real Articulo title" || b.TitleSource != titleSourceIngest {
			t.Fatalf("nameplate survived an Articulo: bill row = %+v", b)
		}
	})
}

// A dateless proposal never replaces a dated title. (ParseVotation maps an
// unparseable date to the '0000-00-00' sentinel; incoming title_date sorts
// before any real date, so recency keeps the dated title.)
func TestIngestDatelessTitleKeepsDatedOne(t *testing.T) {
	s, _ := newTestSyncer(t)

	ingest(t, s, vd(42350, "15351-07", "Dated title", "2026-02-02"))
	if o := s.upsertVotation(context.Background(), camara.VotationData{
		ExternalID: 42351, Boletin: "15351-07", Subject: "Undated newer title",
		Date: "2026-05-05T14:22:00", VoteDate: "0000-00-00",
	}); o.VotationID == 0 {
		t.Fatalf("ingest failed: %+v", o)
	}

	if b := billRow(t, s, "15351-07"); b.Title != "Dated title" {
		t.Fatalf("dateless proposal replaced a dated title: bill row = %+v", b)
	}
}
