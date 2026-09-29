package camara

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MapVotacion is a pure function translating a verified XML payload into the
// normalized votation record the Syncer persists. Boundary policy:
//   - vote normalization is TOTAL (unknown <Opcion> text -> VoteOther + raw kept)
//   - boletin must match ^\d+-\d+$ after trim, else empty (=> bill_id NULL)
//   - vote_date is the date-only prefix of Fecha; unparseable -> "0000-00-00"
//   - no malformed source value fails ingest

// Vote is the internal closed enum; the individual_votes CHECK mirrors it.
type Vote string

const (
	VoteYes       Vote = "yes"
	VoteNo        Vote = "no"
	VoteAbstain   Vote = "abstain"
	VoteAbsent    Vote = "absent"
	VoteDispensed Vote = "dispensed"
	VotePaired    Vote = "paired"
	VoteOther     Vote = "other"
)

func (v Vote) String() string { return string(v) }

// NormalizeVoteOption maps the <Opcion> text to the closed vote enum.
// Observed source values: Afirmativo, En Contra, Abstencion, No Vota,
// Dispensado; anything else becomes VoteOther with the raw text kept —
// ingest never fails and never silently drops on a new API value.
func NormalizeVoteOption(opcion string) Vote {
	switch strings.ToLower(strings.TrimSpace(opcion)) {
	case "afirmativo":
		return VoteYes
	case "en contra":
		return VoteNo
	case "abstencion", "abstención":
		return VoteAbstain
	case "no vota":
		return VoteAbsent
	case "dispensado", "dispensada":
		return VoteDispensed
	case "pareo":
		return VotePaired
	default:
		return VoteOther
	}
}

var boletinRE = regexp.MustCompile(`^\d+-\d+$`)

// NormalizeBoletin trims and validates the boletín; an unusable value maps
// to "" so the caller stores bill_id = NULL instead of failing the votation.
func NormalizeBoletin(boletin string) string {
	b := strings.TrimSpace(boletin)
	if boletinRE.MatchString(b) {
		return b
	}
	return ""
}

const badDate = "0000-00-00" // sorts out of any real party range

// VoteDate derives the date-only join key from the raw source timestamp.
func VoteDate(fecha string) string {
	t, err := time.Parse("2006-01-02T15:04:05", fecha)
	if err != nil {
		if t, err = time.Parse("2006-01-02", fecha); err != nil {
			return badDate
		}
	}
	return t.Format("2006-01-02")
}

// NormalizedVote is one decoded deputy vote.
type NormalizedVote struct {
	Deputy  DiputadoRef
	Vote    Vote
	RawText string
}

// NormalizedPareo is one detected pair.
type NormalizedPareo struct{ A, B DiputadoRef }

// VotationData is the full normalized unit of work for one votation.
type VotationData struct {
	ExternalID int
	Date       string // raw timestamp as received
	VoteDate   string // date-only derived key
	VoteType   string
	Result     string
	Quorum     string
	Step       string
	Boletin    string // "" => bill_id NULL, matches docs/DESIGN.md gap 2
	Subject    string
	Session    SesionXML
	Totals     Totals
	Votes      []NormalizedVote
	Pareos     []NormalizedPareo
	Warns      []string
}

// Totals mirrors the four API-reported counters (audit copies only).
type Totals struct{ Yes, No, Abstain, Dispensed int }

// ParseVotation normalizes a decoded XML payload; it verifies the deputy
// votes exist (the zero-decoded-rows tripwire), keeps duplicates aside, and
// returns warnings rather than errors for every data-quality observation.
func ParseVotation(x VotacionXML) (VotationData, error) {
	if x.Miss() {
		return VotationData{}, fmt.Errorf("camara: miss votation %d (xsi:nil)", x.ID)
	}
	var d VotationData
	d.ExternalID = x.ID
	d.Date = x.Fecha
	d.VoteDate = VoteDate(x.Fecha)
	d.VoteType = x.Tipo.Value
	d.Result = x.Resultado.Value
	d.Quorum = x.Quorum.Value
	d.Step = x.Tramite.Value
	d.Boletin = NormalizeBoletin(x.Boletin)
	if x.Boletin != "" && d.Boletin == "" {
		d.Warns = append(d.Warns, "malformed boletin: "+x.Boletin)
	}
	d.Subject = x.Articulo
	d.Totals = Totals{x.TotalAfirmativos, x.TotalNegativos, x.TotalAbstenciones, x.TotalDispensados}
	if x.Sesion != nil {
		d.Session = *x.Sesion
	}

	seen := map[int]bool{}
	for _, v := range x.Votos {
		if seen[v.Diputado.DIPID] {
			d.Warns = append(d.Warns, fmt.Sprintf("duplicate vote DIPID %d", v.Diputado.DIPID))
			continue
		}
		seen[v.Diputado.DIPID] = true
		d.Votes = append(d.Votes, NormalizedVote{
			Deputy: v.Diputado, Vote: NormalizeVoteOption(v.Opcion.Value), RawText: strings.TrimSpace(v.Opcion.Value),
		})
	}
	for _, p := range x.Pareos {
		d.Pareos = append(d.Pareos, NormalizedPareo{A: p.Diputado1, B: p.Diputado2})
	}
	if len(x.Votos) > 0 {
		var cast int
		for _, v := range d.Votes {
			if v.Vote != VoteAbsent && v.Vote != VotePaired {
				cast++
			}
		}
		if cast == 0 && len(d.Votes) == 0 && (x.TotalAfirmativos+x.TotalNegativos+x.TotalAbstenciones+x.TotalDispensados) > 0 {
			return VotationData{}, errors.New("camara: totals reported but zero votes decoded — XML contract drift")
		}
	}
	return d, nil
}

// ParseDeputies normalizes a getDiputados / getDiputados_Vigentes payload.
func ParseDeputies(body []byte) ([]DiputadoXML, error) {
	var wrap struct {
		XMLName  xml.Name      `xml:"Diputados"`
		Deputies []DiputadoXML `xml:"Diputado"`
	}
	if err := xmlUnmarshal(body, &wrap); err != nil {
		return nil, err
	}
	return wrap.Deputies, nil
}

// BoletinVotaciones returns the full votation detail records for one bill
// via getVotaciones_Boletin (same shape as the detail endpoint, minus the
// per-vote rows) - an out-of-band completeness-check source of IDs.
func (c *Client) BoletinVotaciones(ctx context.Context, boletin string) ([]VotacionXML, error) {
	body, err := c.get(ctx, "getVotaciones_Boletin", url.Values{"prmBoletin": {boletin}})
	if err != nil {
		return nil, err
	}
	var wrap struct {
		XMLName   xml.Name      `xml:"Votaciones"`
		Votations []VotacionXML `xml:"Votacion"`
	}
	if err := xmlUnmarshal(body, &wrap); err != nil {
		return nil, err
	}
	return wrap.Votations, nil
}

// --- HTTP-bound API methods -------------------------------------------------

// VotacionDetalle fetches one votation by external ID; a miss is ErrNotFound.
func (c *Client) VotacionDetalle(ctx context.Context, id int) (VotacionXML, error) {
	body, err := c.get(ctx, "getVotacion_Detalle", url.Values{"prmVotacionID": {fmt.Sprint(id)}})
	if err != nil {
		return VotacionXML{}, err
	}
	var x VotacionXML
	if err := xmlUnmarshal(body, &x); err != nil {
		return VotacionXML{}, err
	}
	if x.Miss() {
		return VotacionXML{}, ErrNotFound
	}
	return x, nil
}

// DiputadosVigentes returns the current deputies (the ONLY source of the
// active flag — there is no per-record active field in the API).
func (c *Client) DiputadosVigentes(ctx context.Context) ([]DiputadoXML, error) {
	return c.deputiesOp(ctx, "getDiputados_Vigentes")
}

// Diputados returns all-history deputies (same shape, no active field).
func (c *Client) Diputados(ctx context.Context) ([]DiputadoXML, error) {
	return c.deputiesOp(ctx, "getDiputados")
}

func (c *Client) deputiesOp(ctx context.Context, op string) ([]DiputadoXML, error) {
	body, err := c.get(ctx, op, nil)
	if err != nil {
		return nil, err
	}
	return ParseDeputies(body)
}
