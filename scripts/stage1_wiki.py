#!/usr/bin/env python3
"""Stage 1 (docs/AFFILIATIONS_PLAN.md): per-period deputy->party bot draft.

Source: Spanish Wikipedia page per legislature period (machines tables of
current composition), one of three shapes:
  A - LIII/LIV/LV: 2-column "Miembros actuales" (8 cells per row)
  B - LV/LVI: rows of one deputy plus one party under a rowspan field.
  C - LVII: 'Listado de diputados' Dist/Diputado/Coalición/Partido per row

Output:
  data/affiliations.review.<period>.csv   per-period review sheet
  data/affiliations.draft.wiki.json       bot draft (curated schema footer)
  data/affiliations.review.report.txt     everything for the human
"""
import csv
import json
import os
import re
import sys
import time
import unicodedata
import urllib.parse
import urllib.request

PERIODS = {
    "2010-2014": ("LIII periodo legislativo del Congreso Nacional de Chile", "A"),
    "2014-2018": ("LIV periodo legislativo del Congreso Nacional de Chile", "A"),
    "2018-2022": ("LV periodo legislativo del Congreso Nacional de Chile", "B"),
    "2022-2026": ("LVI periodo legislativo del Congreso Nacional de Chile", "B"),
    "2026-2030": ("LVII periodo legislativo del Congreso Nacional de Chile", "C"),
}
PERIOD_STARTS = {"2010-2014": "2010-03-11", "2014-2018": "2014-03-11",
                 "2018-2022": "2018-03-11", "2022-2026": "2022-03-11",
                 "2026-2030": "2026-03-11"}
PERIOD_ENDS = {"2010-2014": "2014-03-10", "2014-2018": "2018-03-10",
               "2018-2022": "2022-03-10", "2022-2026": "2026-03-10",
               "2026-2030": None}

UA = {"User-Agent": "Mozilla/5.0 (X11; Lemon x86_64) affiliation-draft/1.0"}

PARTY_MAP = {
    # wiki short code -> our parties short_name
    "PS": "PS", "PCCh": "PC", "PC": "PC", "PDC": "DC", "PPD": "PPD",
    "PRSD": "PR", "RN": "RN", "UDI": "UDI", "EVOP": "EVOPOLI",
    "Ind-EVOP": "EVOPOLI", "Ind-RN": "RN", "Ind-PPD": "PPD",
    "Ind-PR": "PR", "PL": "PL", "FREVS": "FRVS", "RD": "RD",
    "CS": "FA", "COM": "FA", "COMUNES": "FA", "PH": "PH",
    "PRCh": "REP", "PEV": "EVOPOLI", "Unir": "PL", "PDG": "PDG",
    "NLI": "NLI", "RF": "RF", "Ind": "IND",
    "Candidato independiente": "IND", "Independiente": "IND",
}


def norm(s):
    s = unicodedata.normalize("NFKD", s or "")
    s = "".join(c for c in s if not unicodedata.combining(c))
    return re.sub(r"\s+", " ", s).strip().lower()


def fetch(url):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8")


def wikitext(title):
    url = ("https://es.wikipedia.org/w/api.php?action=parse&prop=wikitext&format=json&page="
           + urllib.parse.quote(title))
    return json.loads(fetch(url))["parse"]["wikitext"]["*"]


def links_in(cell):
    return re.findall(r"\[\[([^\]]+)\]\]", cell)


def style_a(seg):
    """LIII/LIV: rows with 4 pairs; positions 2/3 and 6/7."""
    out = []
    for row in seg.split("|-"):
        cells = [c.strip() for c in row.split("||") if c.strip()]
        cells = [c for c in cells if not c.startswith("-")]
        for base in (0, 4):
            if len(cells) < base + 4:
                continue
            dep, party = cells[base + 2], cells[base + 3]
            deps = [d.split("|")[-1] for d in links_in(dep)]
            parts = links_in(party)
            if len(deps) != len(parts) or not deps:
                continue
            for d, p in zip([de.strip() for de in deps], parts):
                short = p.split("|")[-1].strip()
                full = p.split("|")[0].strip()
                out.append((d, full, short))
    return out


def style_b(seg):
    """LV/LVI: each block of 2 lines = one deputy +from one deputy plus one party."""
    out = []
    for row in seg.split("|-"):
        lines = [l for l in row.strip().split("\n") if l.startswith("|[")]
        if len(lines) == 2:
            d = links_in(lines[0])
            p = links_in(lines[1])
            if len(d) == 1 and len(p) == 1:
                short = p[0].split("|")[-1].strip()
                full = p[0].split("|")[0]
                out.append((d[0].split("|")[-1].strip(), full, short))
    return out


def style_c(seg):
    """LVII listado: per | cell rows; take deputy then party link pairs."""
    out = []
    for row in seg.split("|-"):
        lines = [l.strip() for l in row.strip().split("\n") if l.startswith("|")]
        if len(lines) < 4:
            continue
        # skip vote-percentage cells
        cells = [re.sub(r"&nbsp;.*", "", c) for c in lines]
        dep = [re.sub(r"\[d\]|sup[^ ]*|\[\[.*?\]\]", "", c.strip()) for c in cells]
        for i, c in enumerate(cells):
            if i + 3 > len(cells) - 1:
                break
            d_links = links_in(cells[i])
            p_links = links_in(cells[i + 1]) if i + 1 < len(cells) else []
            if d_links and p_links and len(d_links) == 1 and len(p_links) >= 1:
                short = p_links[0].split("|")[-1].strip()
                full = p_links[0].split("|")[0]
                nums = links_in(c)
                if nums is None:
                    pass
                out.append((d_links[0].split("|")[-1].strip(), full, short))
    return out


def extract(period, page, style):
    t = wikitext(page)
    if style == "A":
        k = t.find("==Cámara de Diputados==")
        if k < 0:
            k = t.find("== Cámara de Diputados ==")
        seg = t[k:] if k >= 0 else ""
        seg = re.sub(r"<ref[^>]*/>", "", seg)
        seg = re.sub(r"<ref[^>]*>.*?</ref>", "", seg, flags=re.S)
        m = seg.find("=== Miembros ===")
        seg = seg[m:] if m >= 0 else seg
    elif style == "B":
        k = t.find("== Cámara de Diputados ==")
        seg = t[t.find("=== Miembros ===", k):] if k >= 0 else t
    else:
        k = t.find("== Listado de diputados ==")
        seg = t[k:] if k >= 0 else t
    fn = {"A": style_a, "B": style_b, "C": style_c}[style]
    return fn(seg)


def load_activity():
    """Min/max vote_date per external_id from our own ingested history -
    used as a same-name-family tie-breaker (no external source needed)."""
    import sqlite3
    db = os.path.join("data", "congress.db")
    if not os.path.exists(db):
        return {}
    con = sqlite3.connect(db)
    rows = con.execute(
        "SELECT r.external_id, min(v.vote_date), max(v.vote_date) "
        "FROM individual_votes iv "
        "JOIN representatives r ON r.id = iv.representative_id "
        "JOIN votations v ON v.id = iv.votation_id "
        "GROUP BY r.external_id").fetchall()
    con.close()
    return {ext: (lo, hi) for ext, lo, hi in rows}


def main():
    os.chdir(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    parties = json.load(open("data/parties.json"))
    short_by_full_lower = {norm(p["name"]): p["short_name"] for p in parties}
    short_by_short_lower = {norm(p["short_name"]): p["short_name"] for p in parties}

    rosters = json.load(open("data/deputies_roster.json"))
    seen_ids = set()
    rosters = [r for r in rosters
               if not (r["external_id"] in seen_ids or seen_ids.add(r["external_id"]))]
    activity = load_activity()

    def attach_check(cand_ids, period):
        """Tie-break equal DIPIDs using the per-DIPID vote activity window
        from our own ingest history (no external source needed)."""
        if len(cand_ids) <= 1:
            return cand_ids
        p_lo, p_hi = PERIOD_STARTS[period], PERIOD_ENDS[period] or "2999-01-01"
        kept = [str(cid) for cid in cand_ids
                if (str(cid) in activity
                    and activity[str(cid)][0] <= p_hi
                    and activity[str(cid)][1] >= p_lo)]
        return kept or [str(c) for c in cand_ids]

    # index helpers
    last2 = {}
    first_last3 = {}
    name_full = {}
    for r in rosters:
        ext = r["external_id"]
        toks = r["name"].split()
        name_full[norm(r["name"])] = ext
        if len(toks) >= 2:
            last2.setdefault(norm(" ".join(toks[-2:])), []).append(ext)
        if len(toks) >= 3:
            first_last3.setdefault(norm(toks[0]) + "|" + norm(" ".join(toks[-2:])), []).append(ext)

    draft = []
    unmatched = []
    report = []
    seen_global = set()
    for period, (page, style) in PERIODS.items():
        rows = extract(period, page, style)
        seen = set()
        kept = []
        for name, full, short in rows:
            k = (name, short)
            if k in seen:
                continue
            seen.add(k)
            kept.append((name, full, short))
        print(period, "rows:", len(rows), "->", len(kept))
        with open(f"data/affiliations.review.{period}.csv", "w", newline="") as f:
            wr = csv.writer(f)
            wr.writerow(["wiki_name", "party_full", "party_short", "party", "period",
                         "external_id", "status", "source"])
            for name, full, short in kept:
                got = PARTY_MAP.get(short)
                if got is None:
                    got = short_by_short_lower.get(norm(short)) or short_by_full_lower.get(norm(full))
                if got is None:
                    got = "??" + short
                    report.append(f"{period}: unmapped party {short!r} (full {full!r}) for {name}")
                toks = name.split()
                cand_ids = []
                if len(toks) >= 3:
                    cand_ids = [name_full.get(norm(name))] if norm(name) in name_full else []
                    if not cand_ids:
                        cand_ids = first_last3.get(norm(toks[0]) + "|" + norm(" ".join(toks[-2:])), [])
                    if not cand_ids:
                        cand_ids = last2.get(norm(" ".join(toks[-2:])), [])
                else:
                    cand_ids = last2.get(norm(" ".join(toks[-2:])), [])
                if len(cand_ids) > 1:
                    cand_ids = attach_check(cand_ids, period)
                    if len(cand_ids) > 1:
                        report.append(f"{period}: AMBIGUOUS after activity tie-break {name} -> ids {cand_ids}")
                if not cand_ids:
                    unmatched.append((period, name, short, full))
                    external_id = ""
                elif len(cand_ids) > 1:
                    external_id = ""
                else:
                    external_id = str(cand_ids[0])
                wr.writerow([name, full, short, got, period, external_id,
                             "matched" if external_id else "unmatched", page])
                if external_id:
                    gk = (period, external_id)
                    if gk not in seen_global:
                        seen_global.add(gk)
                        draft.append({
                            "chamber": "camara", "external_id": external_id,
                            "party": got, "start": PERIOD_STARTS[period],
                            "end": PERIOD_ENDS[period],
                        })
        time.sleep(6)

    with open("data/affiliations.draft.wiki.json", "w") as f:
        json.dump({
            "_note": "BOT DRAFT (wikipedia rosters) - human review before `sync -seed`",
            "_generated_at": os.popen("date -Iseconds").read().strip(),
            "unmatched": [{"period": p[0], "name": p[1], "party": p[2]} for p in unmatched],
            "affiliations": draft,
        }, f, ensure_ascii=False, indent=2)
    with open("data/affiliations.review.wiki.report.txt", "w") as f:
        f.write("\n".join(report) + "\n")
    print(f"matched={len(draft)} unmatched={len(unmatched)} problems={len(report)}")


if __name__ == "__main__":
    main()


def write_draft():
    with open("data/affiliations.draft.wiki.json", "w") as f:
        json.dump({
            "_note": "BOT DRAFT (wikipedia rosters) - human review before `sync -seed`",
            "_generated_at": os.popen("date -Iseconds").read().strip(),
            "unmatched": [{"period": p[0], "name": p[1], "party": p[2]} for p in unmatched],
            "affiliations": draft,
        }, f, ensure_ascii=False, indent=2)
    with open("data/affiliations.review.wiki.report.txt", "w") as f:
        f.write("\n".join(report) + "\n")
    print(f"\nmatched={len(draft)} unmatched={len(unmatched)} problems={len(report)}")
