#!/usr/bin/env python3
"""Writes, on the standard output, the SQL of db/migrations/00062_areas_seed.sql: the states of the
United Nations and, for Spain, its autonomous communities, provinces and municipalities.

    scripts/generate-areas-seed.py INE_DICCIONARIO.xlsx COUNTRIES_ES.json > db/migrations/00062_areas_seed.sql

Sources (both free to reuse with attribution):

- INE (Instituto Nacional de Estadística), "Relación de municipios y códigos por comunidades
  autónomas y provincias", https://www.ine.es/daco/daco42/codmun/diccionario25.xlsx (1 January 2025).
- The names of the countries of ISO 3166-1, in Spanish, as CLDR gives them (through
  https://github.com/umpirsky/country-list, data/es/country.json: a JSON object {"ES": "España", …}),
  of which only the 195 states of the UN are seeded (see UN_STATES).

The migration is the result, not this script: it is here to say where the data comes from and to make
the next edition of the INE list easy to bring in (as a new migration). The INE lists Ceuta and Melilla
as provinces with one municipality each, but they are autonomous cities with nothing below them:
migration 00074 took those two levels out of what this script wrote, so a new edition has to leave
them out too.

norm_name must be what `areas.Normalize` gives in Go for the same name (a test checks it).
"""
import json
import re
import sys
import unicodedata
import zipfile

KIND_COUNTRY = "País"

# The 195 states of the United Nations, by their ISO 3166-1 alpha-2 code: its 193 members
# (https://www.un.org/en/about-us/member-states, November 2026) and its two observers, the Holy See (VA)
# and the State of Palestine (PS). The rest of ISO 3166-1 — 54 entries — are dependent territories and
# special areas, not countries (Gibraltar, Hong Kong, Puerto Rico, Greenland, Antarctica, Western Sahara…)
# and Taiwan, which is not a member of the UN: they are left out of this seed. Migration 00077 adds 51 of
# them below the state that administers each, and 00078 adds Antarctica, Western Sahara and Taiwan as
# roots of kind "Territorio".
UN_STATES = """
    AD AE AF AG AL AM AO AR AT AU AZ BA BB BD BE BF BG BH BI BJ BN BO BR BS BT BW BY BZ CA CD CF
    CG CH CI CL CM CN CO CR CU CV CY CZ DE DJ DK DM DO DZ EC EE EG ER ES ET FI FJ FM FR GA GB GD
    GE GH GM GN GQ GR GT GW GY HN HR HT HU ID IE IL IN IQ IR IS IT JM JO JP KE KG KH KI KM KN KP
    KR KW KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MG MH MK ML MM MN MR MT MU MV MW MX MY
    MZ NA NE NG NI NL NO NP NR NZ OM PA PE PG PH PK PL PS PT PW PY QA RO RS RU RW SA SB SC SD SE
    SG SI SK SL SM SN SO SR SS ST SV SY SZ TD TG TH TJ TL TM TN TO TR TT TV TZ UA UG US UY UZ VA
    VC VE VN VU WS YE ZA ZM ZW
""".split()
KIND_COMMUNITY = "Comunidad autónoma"
KIND_CITY = "Ciudad autónoma"
KIND_PROVINCE = "Provincia"
KIND_MUNICIPALITY = "Municipio"

# The letters that are not a base letter and a mark, so unicode normalization does not split them.
EXTRA = {"ß": "ss", "æ": "ae", "œ": "oe", "ø": "o", "ł": "l", "đ": "d", "ð": "d", "þ": "th", "ı": "i"}


def normalize(text: str) -> str:
    """Lower case, no accents, and every run of anything but letters and digits as one space."""
    out = []
    for char in unicodedata.normalize("NFD", text.lower()):
        if unicodedata.category(char) == "Mn":
            continue
        out.append(EXTRA.get(char, char))
    folded = "".join(out)
    return re.sub(r"[^\w]+", " ", folded, flags=re.UNICODE).replace("_", " ").strip()


# INE code of the autonomous community -> (name, kind, ISO 3166-2:ES code).
COMMUNITIES = {
    "01": ("Andalucía", KIND_COMMUNITY),
    "02": ("Aragón", KIND_COMMUNITY),
    "03": ("Principado de Asturias", KIND_COMMUNITY),
    "04": ("Islas Baleares", KIND_COMMUNITY),
    "05": ("Canarias", KIND_COMMUNITY),
    "06": ("Cantabria", KIND_COMMUNITY),
    "07": ("Castilla y León", KIND_COMMUNITY),
    "08": ("Castilla-La Mancha", KIND_COMMUNITY),
    "09": ("Cataluña", KIND_COMMUNITY),
    "10": ("Comunidad Valenciana", KIND_COMMUNITY),
    "11": ("Extremadura", KIND_COMMUNITY),
    "12": ("Galicia", KIND_COMMUNITY),
    "13": ("Comunidad de Madrid", KIND_COMMUNITY),
    "14": ("Región de Murcia", KIND_COMMUNITY),
    "15": ("Comunidad Foral de Navarra", KIND_COMMUNITY),
    "16": ("País Vasco", KIND_COMMUNITY),
    "17": ("La Rioja", KIND_COMMUNITY),
    "18": ("Ceuta", KIND_CITY),
    "19": ("Melilla", KIND_CITY),
}

# INE code of the province -> (name, INE code of its community). The names of the communities and the
# provinces are the Spanish ones ("Álava", "Gerona", "Islas Baleares"), not the INE's, which are in the
# language of the territory or in two ("Araba/Álava", "Girona", "Illes Balears"): migration 00079 changed
# them in the database, and this keeps a regenerated seed the same. The municipalities keep the INE's.
PROVINCES = {
    "01": ("Álava", "16"), "02": ("Albacete", "08"), "03": ("Alicante", "10"),
    "04": ("Almería", "01"), "05": ("Ávila", "07"), "06": ("Badajoz", "11"), "07": ("Islas Baleares", "04"),
    "08": ("Barcelona", "09"), "09": ("Burgos", "07"), "10": ("Cáceres", "11"), "11": ("Cádiz", "01"),
    "12": ("Castellón", "10"), "13": ("Ciudad Real", "08"), "14": ("Córdoba", "01"),
    "15": ("La Coruña", "12"), "16": ("Cuenca", "08"), "17": ("Gerona", "09"), "18": ("Granada", "01"),
    "19": ("Guadalajara", "08"), "20": ("Guipúzcoa", "16"), "21": ("Huelva", "01"), "22": ("Huesca", "02"),
    "23": ("Jaén", "01"), "24": ("León", "07"), "25": ("Lérida", "09"), "26": ("La Rioja", "17"),
    "27": ("Lugo", "12"), "28": ("Madrid", "13"), "29": ("Málaga", "01"), "30": ("Murcia", "14"),
    "31": ("Navarra", "15"), "32": ("Orense", "12"), "33": ("Asturias", "03"), "34": ("Palencia", "07"),
    "35": ("Las Palmas", "05"), "36": ("Pontevedra", "12"), "37": ("Salamanca", "07"),
    "38": ("Santa Cruz de Tenerife", "05"), "39": ("Cantabria", "06"), "40": ("Segovia", "07"),
    "41": ("Sevilla", "01"), "42": ("Soria", "07"), "43": ("Tarragona", "09"), "44": ("Teruel", "02"),
    "45": ("Toledo", "08"), "46": ("Valencia", "10"), "47": ("Valladolid", "07"),
    "48": ("Vizcaya", "16"), "49": ("Zamora", "07"), "50": ("Zaragoza", "02"), "51": ("Ceuta", "18"),
    "52": ("Melilla", "19"),
}

# The INE writes "Coruña, A" and "Hospitalet de Llobregat, L'": the article goes first again.
ARTICLE = re.compile(r"^(.+), (el|la|los|las|l'|l’|a|o|os|as|els|les|sa|ses|es|s'|s’)$", re.IGNORECASE)


# The same in a name in two languages: "Vila Joiosa, la/Villajoyosa" is "La Vila Joiosa/Villajoyosa".
ARTICLE_BILINGUAL = re.compile(r"^(.+), (el|la|los|las|l'|l’|a|o|os|as|els|les|sa|ses|es|s'|s’)/(.+)$", re.IGNORECASE)


def with_article(article: str, base: str) -> str:
    article = article[0].upper() + article[1:]
    return article + base if article[-1] in "'’" else f"{article} {base}"


def readable(name: str) -> str:
    match = ARTICLE_BILINGUAL.match(name)
    if match:
        base, article, other = match.groups()
        return f"{with_article(article, base)}/{other}"
    match = ARTICLE.match(name)
    if match:
        base, article = match.groups()
        return with_article(article, base)
    return name


def sql(text: str) -> str:
    return "'" + text.replace("'", "''") + "'"


def read_ine(path):
    """The municipalities of the INE list: (province code, community code, name, INE code)."""
    z = zipfile.ZipFile(path)
    shared = [
        "".join(re.findall(r"<t[^>]*>(.*?)</t>", s, re.S))
        for s in re.findall(r"<si>(.*?)</si>", z.read("xl/sharedStrings.xml").decode(), re.S)
    ]
    rows = re.findall(r"<row [^>]*>(.*?)</row>", z.read("xl/worksheets/sheet1.xml").decode(), re.S)
    out = []
    for row in rows:
        cells = {}
        for m in re.finditer(r'<c r="([A-Z]+)\d+"([^>]*?)(?:/>|>(.*?)</c>)', row, re.S):
            column, attrs, inner = m.groups()
            value = re.search(r"<v>(.*?)</v>", inner or "")
            if value:
                cells[column] = shared[int(value.group(1))] if 't="s"' in attrs else value.group(1)
        if cells.get("A", "").isdigit() and "E" in cells:
            name = cells["E"].replace("&amp;", "&").replace("&apos;", "'").replace("&quot;", '"')
            out.append((cells["B"], cells["A"], name, cells["B"] + cells["C"]))
    return out


def main(ine_path, countries_path):
    municipalities = read_ine(ine_path)
    all_countries = json.load(open(countries_path, encoding="utf-8"))
    assert len(UN_STATES) == len(set(UN_STATES)) == 195, "the list of states of the UN is not 195 different codes"
    missing = sorted(set(UN_STATES) - set(all_countries))
    assert not missing, f"no name in Spanish for {missing}"
    countries = {code: all_countries[code] for code in UN_STATES}
    lines = ["-- +goose Up"]
    lines.append(
        """-- The zones of the places: the 195 states of the United Nations (its 193 members and its two
-- observers, the Holy See and Palestine) and, for Spain, its autonomous communities, provinces and
-- municipalities, so that a place can say where it is without anybody typing a name. Other countries get
-- their own zones as the places that are in them are added, and so does a territory that is not a state
-- (Gibraltar, for one), which goes below the state that administers it.
--
-- Sources (free to reuse with attribution):
--   * INE (Instituto Nacional de Estadística), "Relación de municipios y códigos por comunidades
--     autónomas y provincias", 1 January 2025: https://www.ine.es/daco/daco42/codmun/diccionario25.xlsx
--   * The UN member states (https://www.un.org/en/about-us/member-states) and observers, by their ISO
--     3166-1 alpha-2 code, with their names in Spanish from CLDR (unicode.org).
-- The code of a country is its ISO 3166-1 alpha-2, that of a community or a province is the INE's, and
-- that of a municipality is the INE's five digits (the code of its province and its own three). The names
-- the INE writes with the article last ("Coruña, A") are written with it first. This file is generated by
-- scripts/generate-areas-seed.py.
"""
    )
    # Countries.
    rows = []
    for code, name in sorted(countries.items(), key=lambda kv: normalize(kv[1])):
        rows.append(f"    (NULL, {sql(KIND_COUNTRY)}, {sql(name)}, {sql(normalize(name))}, {sql(code)})")
    lines.append("INSERT INTO areas (parent_id, kind, name, norm_name, code) VALUES\n" + ",\n".join(rows) + ";\n")

    spain = "(SELECT id FROM areas WHERE parent_id IS NULL AND code = 'ES')"
    # Communities.
    rows = [
        f"    ({sql(name)}, {sql(normalize(name))}, {sql(code)}, {sql(kind)})"
        for code, (name, kind) in COMMUNITIES.items()
    ]
    lines.append(
        "INSERT INTO areas (parent_id, kind, name, norm_name, code)\n"
        f"SELECT {spain}, v.kind, v.name, v.norm_name, v.code\n"
        "FROM (VALUES\n" + ",\n".join(rows) + "\n) AS v(name, norm_name, code, kind);\n"
    )
    # Provinces.
    rows = [
        f"    ({sql(community)}, {sql(name)}, {sql(normalize(name))}, {sql(code)})"
        for code, (name, community) in PROVINCES.items()
    ]
    lines.append(
        "INSERT INTO areas (parent_id, kind, name, norm_name, code)\n"
        f"SELECT c.id, {sql(KIND_PROVINCE)}, v.name, v.norm_name, v.code\n"
        "FROM (VALUES\n" + ",\n".join(rows) + "\n) AS v(community, name, norm_name, code)\n"
        f"JOIN areas c ON c.parent_id = {spain} AND c.code = v.community;\n"
    )
    # Municipalities, one statement per province.
    by_province = {}
    seen = set()
    for province, community, name, code in municipalities:
        assert province in PROVINCES and PROVINCES[province][1] == community, (province, community, name)
        shown = readable(name)
        key = (province, normalize(shown))
        assert key not in seen, f"two municipalities of the province {province} are the same: {shown}"
        seen.add(key)
        by_province.setdefault(province, []).append((shown, normalize(shown), code))
    assert sorted(by_province) == sorted(PROVINCES), "a province has no municipalities"
    for province in sorted(by_province):
        rows = [f"    ({sql(n)}, {sql(norm)}, {sql(c)})" for n, norm, c in sorted(by_province[province], key=lambda r: r[2])]
        lines.append(
            f"-- {PROVINCES[province][0]}\n"
            "INSERT INTO areas (parent_id, kind, name, norm_name, code)\n"
            f"SELECT p.id, {sql(KIND_MUNICIPALITY)}, v.name, v.norm_name, v.code\n"
            "FROM (VALUES\n" + ",\n".join(rows) + "\n) AS v(name, norm_name, code)\n"
            f"JOIN areas p ON p.kind = {sql(KIND_PROVINCE)} AND p.code = {sql(province)};\n"
        )
    lines.append("-- +goose Down\nDELETE FROM areas;\n")
    sys.stdout.write("\n".join(lines))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2])
