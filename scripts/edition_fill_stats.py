#!/usr/bin/env python3
"""
edition_fill_stats.py — диагностика заполняемости edition-полей в реальной
fb2-коллекции. Семплит N fb2-файлов из zip-архивов и считает, у скольких
заполнены атрибуты издания, на которые завязана группировка изданий в книги
(Phase 2): ISBN, src-title-info (переводы), translator, publisher, год издания,
document-info/id.

Зависимости: только стандартная библиотека Python 3.8+ (zipfile, ElementTree,
codecs — windows-1251 встроен). Ни Go, ни БД, ни исходников skriptes не нужно.

Запуск (host):
    python3 edition_fill_stats.py /path/to/books            # 3000 файлов
    python3 edition_fill_stats.py /path/to/books 8000 42    # 8000 файлов, seed 42

Запуск (docker, если на хосте нет python):
    docker run --rm -v /path/to/books:/books:ro \
        -v "$PWD/edition_fill_stats.py:/scan.py:ro" \
        python:3-alpine python /scan.py /books

BOOKS_DIR — каталог, где лежат .zip с fb2 (на хосте это BOOKS_HOST_PATH из
infra/.env; в контейнере backend это /data/books).
"""
import os
import sys
import random
import zipfile
import xml.etree.ElementTree as ET

# Wildcard-namespace path: fb2 объявляет default ns, тег приходит как {ns}tag.
# Python 3.8+ понимает {*} в ElementPath — матчим по local-name.


def first_text(parent, tag):
    if parent is None:
        return ""
    el = parent.find("{*}" + tag)
    if el is None or el.text is None:
        return ""
    return el.text.strip()


def person_name(el):
    """Собрать display-имя 'Фамилия Имя Отчество' из <author>/<translator>."""
    if el is None:
        return ""
    parts = [p for p in (first_text(el, "last-name"),
                         first_text(el, "first-name"),
                         first_text(el, "middle-name")) if p]
    if parts:
        return " ".join(parts)
    return first_text(el, "nickname")


def norm_isbn(s):
    t = "".join(c for c in (s or "").upper() if c.isdigit() or c == "X")
    return t if len(t) in (10, 13) else ""


def parse_fb2(data):
    """Вернуть dict извлечённых полей или None при неудачном парсинге."""
    root = None
    try:
        root = ET.fromstring(data)
    except ET.ParseError:
        # fallback: часто кодировка windows-1251 + кривой декл/хвост тела.
        try:
            txt = data.decode("cp1251", errors="replace")
            # обрежем по концу </description> — нам нужен только заголовок,
            # и тело часто содержит то, что роняет парсер.
            i = txt.find("</description>")
            if i != -1:
                head = txt[:i + len("</description>")]
                # подставим закрывающий корень, чтобы XML был валиден
                head = head + "</FictionBook>"
                # уберём xml-декларацию с encoding (мы уже декодировали в str)
                if head.lstrip().startswith("<?xml"):
                    head = head[head.find("?>") + 2:]
                root = ET.fromstring(head)
            else:
                return None
        except Exception:
            return None
    desc = root.find("{*}description")
    if desc is None:
        return None
    ti = desc.find("{*}title-info")
    sti = desc.find("{*}src-title-info")
    pi = desc.find("{*}publish-info")
    di = desc.find("{*}document-info")

    src_lang = first_text(ti, "src-lang") or first_text(sti, "lang")
    return {
        "title_lang":    first_text(ti, "lang"),
        "translator":    person_name(ti.find("{*}translator")) if ti is not None else "",
        "isbn":          norm_isbn(first_text(pi, "isbn")),
        "publisher":     first_text(pi, "publisher"),
        "edition_title": first_text(pi, "book-name"),
        "edition_year":  first_text(pi, "year"),
        "src_title":     first_text(sti, "book-title"),
        "src_author":    person_name(sti.find("{*}author")) if sti is not None else "",
        "src_lang":      src_lang,
        "fb2_doc_id":    first_text(di, "id"),
    }


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)
    books_dir = sys.argv[1]
    sample_n = int(sys.argv[2]) if len(sys.argv) > 2 else 3000
    seed = int(sys.argv[3]) if len(sys.argv) > 3 else 1
    rnd = random.Random(seed)

    zips = []
    for dirpath, _, files in os.walk(books_dir):
        for f in files:
            if f.lower().endswith(".zip"):
                zips.append(os.path.join(dirpath, f))
    if not zips:
        print(f"Не найдено .zip в {books_dir}", file=sys.stderr)
        sys.exit(2)
    rnd.shuffle(zips)
    print(f"Архивов найдено: {len(zips)}; цель семпла: {sample_n} fb2-файлов\n")

    fields = ["isbn", "src_title", "src_author", "src_lang", "translator",
              "publisher", "edition_title", "edition_year", "fb2_doc_id", "title_lang"]
    filled = {k: 0 for k in fields}
    examples = {k: [] for k in fields}
    title_lang_dist = {}
    src_lang_dist = {}
    scanned = 0
    parse_fail = 0
    # композитные сигналы
    is_translation = 0          # любой признак перевода
    full_xlang_key = 0          # src_author + src_title + src_lang (Tier-1 cross-lang)

    for zp in zips:
        if scanned >= sample_n:
            break
        try:
            zf = zipfile.ZipFile(zp)
        except Exception:
            continue
        names = [n for n in zf.namelist() if n.lower().endswith(".fb2")]
        rnd.shuffle(names)
        # берём до 50 файлов из архива, чтобы семпл был разбросан по коллекции
        for name in names[:50]:
            if scanned >= sample_n:
                break
            try:
                data = zf.read(name)
            except Exception:
                continue
            rec = parse_fb2(data)
            scanned += 1
            if rec is None:
                parse_fail += 1
                continue
            for k in fields:
                if rec[k]:
                    filled[k] += 1
                    if len(examples[k]) < 5:
                        examples[k].append(rec[k][:80])
            if rec["title_lang"]:
                title_lang_dist[rec["title_lang"]] = title_lang_dist.get(rec["title_lang"], 0) + 1
            if rec["src_lang"]:
                src_lang_dist[rec["src_lang"]] = src_lang_dist.get(rec["src_lang"], 0) + 1
            if rec["src_title"] or rec["translator"] or rec["src_lang"]:
                is_translation += 1
            if rec["src_title"] and rec["src_author"] and rec["src_lang"]:
                full_xlang_key += 1
        zf.close()

    ok = scanned - parse_fail
    print(f"Просканировано: {scanned}  |  распарсилось: {ok}  |  ошибок парсинга: {parse_fail}\n")
    if ok == 0:
        print("Нечего считать (0 распарсилось).")
        return

    def pct(n):
        return f"{n:6d}  {100.0 * n / ok:5.1f}%"

    print("Заполняемость полей (от успешно распарсенных):")
    for k in fields:
        print(f"  {k:14s} {pct(filled[k])}")

    print("\nКлючи группировки (композитные):")
    print(f"  похоже на перевод (src/translator/src-lang) {pct(is_translation)}")
    print(f"  ПОЛНЫЙ cross-lang ключ (src_title+author+lang) {pct(full_xlang_key)}")
    print(f"  ISBN (валидный 10/13)                        {pct(filled['isbn'])}")
    print(f"  document-info/id (точный дубль)              {pct(filled['fb2_doc_id'])}")

    def top(d, n=8):
        return ", ".join(f"{k}={v}" for k, v in sorted(d.items(), key=lambda x: -x[1])[:n])
    print("\ntitle-info/lang топ:", top(title_lang_dist))
    print("src-lang топ:       ", top(src_lang_dist))

    print("\nПримеры значений:")
    for k in fields:
        if examples[k]:
            print(f"  {k}:")
            for e in examples[k]:
                print(f"      {e}")


if __name__ == "__main__":
    main()
