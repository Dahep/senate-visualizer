// Package camara is a client for the Cámara de Diputados open-data API.
//
// Client contract (docs/DESIGN.md 0011): path-style invocation
// <base>/<op>?prm... (the ?op= form returns the ASMX HTML help page, not
// XML). Every response must be text/xml (fallback: first non-space byte
// '<'); an HTML body is classified retryable and never decoded. The request
// timeout is context-bound; at most maxRetries with exponential backoff on
// 429/5xx/network; a terminal failure is reported to the caller unchanged.
package camara

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is returned when a getVotacion_Detalle miss is detected (the
// API answers a nil <Votacion xsi:nil="true"/> with HTTP 200).
var ErrNotFound = errors.New("camara: not found")

const (
	reqTimeout = 30 * time.Second
	maxRetries = 5
)

// Client is a rate-limited, retrying HTTP client over the XML API.
type Client struct {
	hc      *http.Client
	base    string
	delay   time.Duration
	lastReq time.Time
}

// NewClient returns a client polling base (default: opendata endpoint) with
// at least delay between consecutive calls.
func NewClient(base string, delay time.Duration) *Client {
	if base == "" {
		base = "https://opendata.camara.cl/wscamaradiputados.asmx"
	}
	return &Client{
		hc:    &http.Client{Timeout: reqTimeout},
		base:  base,
		delay: delay,
	}
}

// VotacionXML is the getVotacion_Detalle payload. Field names are live-verified
// (2026-09-15): <ID>, nested <Sesion>, <Boletin>, <Articulo> (can be empty),
// <Tramite>/<Tipo>/<Resultado>/<Quorum> coded text, four <Total*>
// counters, a <Votos><Voto> list (nested <Diputado><DIPID> + <Opcion Codigo>),
// a <Pareos><Pareo> pair list, and <Informe> (persisted later or ignored).
type VotacionXML struct {
	XMLName           xml.Name   `xml:"Votacion"`
	Nil               string     `xml:"nil,attr"` // "true" => miss sentinel
	ID                int        `xml:"ID"`
	Fecha             string     `xml:"Fecha"` // raw, sloppy by design — see mapping
	Tipo              CodedText  `xml:"Tipo"`
	Resultado         CodedText  `xml:"Resultado"`
	Quorum            CodedText  `xml:"Quorum"`
	Sesion            *SesionXML `xml:"Sesion"`
	Boletin           string     `xml:"Boletin"`
	Articulo          string     `xml:"Articulo"`
	Tramite           CodedText  `xml:"Tramite"`
	Informe           CodedText  `xml:"Informe"`
	TotalAfirmativos  int        `xml:"TotalAfirmativos"`
	TotalNegativos    int        `xml:"TotalNegativos"`
	TotalAbstenciones int        `xml:"TotalAbstenciones"`
	TotalDispensados  int        `xml:"TotalDispensados"`
	Votos             []VotoXML  `xml:"Votos>Voto"`
	Pareos            []PareoXML `xml:"Pareos>Pareo"`
}

// CodedText is <X Codigo="n">label</X>. We map on the TEXT value; the code is
// informational (observed: 0=En Contra, 1=Afirmativo, 2=Abstencion,
// 3=Dispensado, 4=No Vota).
type CodedText struct {
	Code  string `xml:"Codigo,attr"`
	Value string `xml:",chardata"`
}

// SesionXML arrives free inside every detail response.
type SesionXML struct {
	ID           int       `xml:"ID"`
	Numero       string    `xml:"Numero"`
	Fecha        string    `xml:"Fecha"`
	FechaTermino string    `xml:"FechaTermino"`
	Tipo         CodedText `xml:"Tipo"`
}

// VotoXML is ONE vote: <Opcion Codigo="0">En Contra</Opcion>. There is NO
// 'OpcionVoto' element; a struct written with the wrong tag decodes to zero
// votes silently (encoding/xml ignores unknown fields) — the ingest totals
// cross-check (zero decoded rows hard-fails) is the tripwire.
type VotoXML struct {
	Diputado DiputadoRef `xml:"Diputado"`
	Opcion   CodedText   `xml:"Opcion"`
}

// DiputadoRef is the deputy identity block inside a <Voto>/<Pareo>.
type DiputadoRef struct {
	DIPID           int    `xml:"DIPID"`
	Nombre          string `xml:"Nombre"`
	ApellidoPaterno string `xml:"Apellido_Paterno"`
	ApellidoMaterno string `xml:"Apellido_Materno"`
}

// PareoXML: deputy pairs, NOT an <Opcion> value.
type PareoXML struct {
	Diputado1 DiputadoRef `xml:"Diputado1"`
	Diputado2 DiputadoRef `xml:"Diputado2"`
}

// DiputadoXML is one record of getDiputados_Vigentes / getDiputados. There is
// no per-record active marker — active status comes from vigentes-list
// membership (the Syncer grants it). Fecha_Nacimiento can be ABSENT.
type DiputadoXML struct {
	DIPID           int       `xml:"DIPID"`
	Nombre          string    `xml:"Nombre"`
	Nombre2         string    `xml:"Nombre2"`
	ApellidoPaterno string    `xml:"Apellido_Paterno"`
	ApellidoMaterno string    `xml:"Apellido_Materno"`
	FechaNacimiento string    `xml:"Fecha_Nacimiento"`
	Sexo            CodedText `xml:"Sexo"`
}

// Miss returns true for the xsi:nil sentinel body of a votation lookup.
func (v VotacionXML) Miss() bool {
	return v.Nil == "true" && v.ID == 0 && len(v.Votos) == 0
}

// get performs one op with pacing, response-shape gating, and bounded retries.
func (c *Client) get(ctx context.Context, op string, params url.Values) ([]byte, error) {
	full := c.base + "/" + op
	if enc := params.Encode(); enc != "" {
		full += "?" + enc
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		c.rateWait()
		var body []byte
		var err error
		body, err = c.once(ctx, full)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable(err) || attempt >= maxRetries {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff(attempt)):
		}
	}
}

func (c *Client) rateWait() {
	if c.delay > 0 {
		if wait := time.Until(c.lastReq.Add(c.delay)); wait > 0 {
			time.Sleep(wait)
		}
	}
	c.lastReq = time.Now()
}

func backoff(attempt int) time.Duration {
	return time.Second << uint(attempt) // 1s, 2s, 4s, 8s, 16s
}

func retryable(err error) bool {
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		return httpErr.status == http.StatusTooManyRequests || httpErr.status >= 500
	}
	return !errors.Is(err, ErrNotFound) && !errors.Is(err, context.Canceled)
}

type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("camara: HTTP %d", e.status)
}

func (c *Client) once(ctx context.Context, full string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{status: resp.StatusCode}
	}
	if !looksXML(resp, body) {
		// HTML help/rate-limit/maintenance pages arrive as text/html.
		return nil, &httpStatusError{status: http.StatusServiceUnavailable}
	}
	return body, nil
}

func looksXML(resp *http.Response, body []byte) bool {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		switch mt, _, _ := mime.ParseMediaType(ct); {
		case strings.Contains(mt, "xml"):
			return true
		case strings.Contains(mt, "html"):
			return false
		}
	}
	return len(body) > 0 && body[0] == '<'
}
