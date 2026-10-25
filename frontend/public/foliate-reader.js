// Логика ридера (iframe /foliate-reader.html). Отдельным файлом, а не inline
// <script>: CSP сайта (frontend/nginx-security-headers.conf) — script-src 'self'
// без 'unsafe-inline'. Страницы книги foliate рендерит в blob:-iframe'ах, они
// наследуют эту CSP — поэтому скрипты внутри книги не выполняются.
//
// Не часть react-bundle'а — статический ассет в public/. Логика:
//
//  1. Читаем ?src=URL&cfi=POS&settings=JSON из URL iframe'а. settings —
//     ReaderFrameSettings (frontend/src/lib/readerSettings.ts): цвета темы,
//     размер шрифта, поля, способы перелистывания; дальше их меняет родитель
//     сообщением {type:'settings'}.
//  2. Скачиваем epub из src (как blob), парсим через foliate-js makeBook.
//  3. Открываем в <foliate-view>, при наличии cfi восстанавливаем позицию.
//  4. Слушаем relocate event → отправляем postMessage родителю
//     {type:'position', cfi, fraction}. Родитель debounce'нуто PUT'ит
//     в /api/books/{id}/position. Дочитал до конца перелистыванием
//     (fraction >= 0.99) — {type:'completed'}, родитель вызывает MarkRead;
//     прыжок слайдером или оглавлением к концу книгу прочитанной не делает.
//     Перелистывание пользователем (тап/свайп/клавиши) → {type:'turned'} —
//     родитель прячет свою панель.
//  5. Перелистывание: свайп — сам foliate-paginator (страница едет за
//     пальцем; атрибут no-swipe выключает), тап по краю — здесь (левые 30 % —
//     назад, правые 30 % — вперёд), тап по центру → {type:'toggle-ui'}
//     (панель ридера). Клавиши ←/→, PageUp/PageDown, пробел. Анимация —
//     атрибут animated; патч paginator.js делает её короткой и
//     неблокирующей (тап во время анимации не теряется).
//  6. Выделения и закладки (#389): выделенный текст → {type:'selection', cfi,
//     text} родителю; родитель хранит заметки и присылает {type:'set-highlights',
//     cfis} — подсвечиваем через overlayer foliate; клик по подсветке →
//     {type:'annotation-click', cfi}; {type:'goto', cfi} — перейти к месту.
//  7. Оглавление и слайдер (#592, #593): после открытия — {type:'toc', items}
//     (плоский список {id, label, href, depth}; в {type:'position'} — tocId
//     текущей главы); {type:'goto', href} — к главе, {type:'goto-fraction',
//     fraction} — к доле книги (слайдер прогресса; шлётся и пока его тянут —
//     страница едет за ползунком, лишние промежуточные цели пропускаются).
//  8. Колонтитулов нет (#600): страница — только текст, глава — в панели,
//     процент — в слайдере.

import './foliate/view.js'
import { Overlayer } from './foliate/overlayer.js'

const params = new URLSearchParams(location.search)
const src = params.get('src')
const initialCfi = params.get('cfi') || ''

const post = msg => parent.postMessage(msg, location.origin)

// Значения по умолчанию — как READER_DEFAULTS (тёмная тема, узкие поля).
const DEFAULT_SETTINGS = {
  bg: '#141414', fg: '#d0d0d0', dark: true,
  fontSize: 100, margin: 8, gap: 4,
  swipe: true, tap: true, animated: true,
}
const parseSettings = raw => {
  try {
    const s = JSON.parse(raw)
    return s && typeof s === 'object' ? { ...DEFAULT_SETTINGS, ...s } : { ...DEFAULT_SETTINGS }
  } catch {
    return { ...DEFAULT_SETTINGS }
  }
}
let settings = parseSettings(params.get('settings'))

const status = document.getElementById('status')
const host = document.getElementById('view-host')

let view = null
let lastCfi = ''
let completedReported = false
// Подсвеченные места (CFI выделений) и документ с текущим выделением.
let highlights = new Set()
let selectionDoc = null
let opened = false

// Стили страницы книги поверх её собственных: цвета темы (принудительно —
// иначе чёрный текст книги на тёмном фоне), размер шрифта, переносы.
const bookCSS = s => `
  @namespace epub "http://www.idpf.org/2007/ops";
  html {
    color-scheme: ${s.dark ? 'dark' : 'light'};
    font-size: ${Number(s.fontSize) || 100}% !important;
  }
  html, body {
    background: ${s.bg} !important;
    color: ${s.fg} !important;
  }
  body * {
    color: inherit !important;
    background-color: transparent !important;
  }
  /* fb2cng рисует таблицы чёрными рамками — на тёмной теме их не видно. */
  table, th, td {
    border-color: color-mix(in srgb, currentColor 40%, transparent) !important;
  }
  a:any-link {
    text-decoration-color: color-mix(in srgb, currentColor 45%, transparent);
  }
  p, li, blockquote, dd {
    -webkit-hyphens: auto;
    hyphens: auto;
    widows: 2;
    orphans: 2;
  }
  pre {
    white-space: pre-wrap !important;
  }
`

const applyPageColors = s => {
  const root = document.documentElement
  root.style.setProperty('--bg', s.bg)
  root.style.setProperty('--fg', s.fg)
  root.style.colorScheme = s.dark ? 'dark' : 'light'
}

const applyRenderer = s => {
  const r = view?.renderer
  if (!r) return
  r.setAttribute('margin', `${Number(s.margin) || 0}px`)
  r.setAttribute('gap', `${Number(s.gap) || 0}%`)
  r.toggleAttribute('animated', Boolean(s.animated))
  r.toggleAttribute('no-swipe', !s.swipe)
  r.setStyles?.(bookCSS(s))
}

applyPageColors(settings)

const applyHighlights = (next) => {
  if (!view || !opened) return
  for (const cfi of highlights) if (!next.has(cfi)) view.deleteAnnotation({ value: cfi }).catch(() => {})
  for (const cfi of next) if (!highlights.has(cfi)) view.addAnnotation({ value: cfi }).catch(() => {})
  highlights = next
}

// Оглавление книги плоским списком с глубиной; id — те же, что у
// relocate.tocItem (их раздаёт foliate при open).
const fileOf = href => (href || '').split('#')[0]
// fb2cng кладёт в оглавление каждую сноску: «Сноски» → 1, 2, … 300 в том же
// файле. Это не главы — сам раздел оставляем, номера сносок нет. (Главы с
// номерами вместо названий — отдельные файлы, их не трогает.)
const isNotesList = it => Boolean(it.subitems?.length) && it.subitems.every(s =>
  !s.subitems?.length && fileOf(s.href) === fileOf(it.href) && /^\d+$/.test((s.label || '').trim()))
const flattenToc = (items, depth = 0, out = []) => {
  for (const it of items ?? []) {
    const label = (it.label || '').trim()
    if (label && it.href) out.push({ id: it.id ?? null, label, href: it.href, depth })
    if (!isNotesList(it)) flattenToc(it.subitems, depth + 1, out)
  }
  return out
}

// Переход к доле книги, пока тянут слайдер: цель обновляется на каждый шаг
// ползунка, а foliate грузит большую главу не мгновенно, — идём к последней
// цели, промежуточные, накопившиеся за переход, пропускаем.
let seekTarget = null
let seeking = false
const seek = async (f) => {
  seekTarget = f
  // Панель со слайдером видна раньше, чем книга встала на стартовое место, —
  // ранний переход дождётся конца открытия (после него seek(seekTarget)).
  if (seeking || !opened) return
  seeking = true
  try {
    while (seekTarget !== null) {
      const target = seekTarget
      seekTarget = null
      await view.goToFraction(target).catch(() => {})
    }
  } finally {
    seeking = false
  }
}

const turn = dir => {
  if (!view) return
  const go = dir === 'left' ? view.goLeft()
    : dir === 'right' ? view.goRight()
      : dir === 'prev' ? view.prev()
        : view.next()
  Promise.resolve(go).catch(() => {})
}

// Сообщения от родителя (ReaderPage) — только с нашего origin.
window.addEventListener('message', (e) => {
  if (e.origin !== location.origin || e.source !== parent) return
  const msg = e.data || {}
  switch (msg.type) {
    case 'set-highlights': {
      const next = new Set(Array.isArray(msg.cfis) ? msg.cfis : [])
      if (!opened) {
        highlights = next
        return
      }
      applyHighlights(next)
      break
    }
    case 'goto': {
      const target = msg.cfi || msg.href
      if (view && target) view.goTo(target).catch(() => {})
      break
    }
    case 'goto-fraction': {
      const f = Number(msg.fraction)
      if (view && Number.isFinite(f)) seek(Math.min(1, Math.max(0, f)))
      break
    }
    case 'clear-selection':
      selectionDoc?.getSelection()?.removeAllRanges()
      selectionDoc = null
      break
    case 'settings':
      settings = { ...DEFAULT_SETTINGS, ...(msg.settings || {}) }
      applyPageColors(settings)
      applyRenderer(settings)
      break
    case 'turn':
      turn(msg.dir)
      break
  }
})

// ── Клавиатура ──────────────────────────────────────────────────────────
const KEYS = {
  ArrowLeft: 'left', ArrowRight: 'right',
  ArrowUp: 'prev', ArrowDown: 'next',
  PageUp: 'prev', PageDown: 'next',
}
const onKeyDown = (e) => {
  if (!view || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
  const el = e.target
  if (el?.isContentEditable || /^(input|textarea|select)$/i.test(el?.tagName ?? '')) return
  const dir = e.key === ' ' ? (e.shiftKey ? 'prev' : 'next') : KEYS[e.key]
  if (!dir) return
  e.preventDefault()
  turn(dir)
}
window.addEventListener('keydown', onKeyDown)

// ── Тапы ────────────────────────────────────────────────────────────────
// Тап = указатель отпущен почти там же и быстро, одним пальцем. pointerup, а
// не click: iOS не шлёт click по обычному тексту без обработчика на элементе.
// Сдвиг больше TAP_SLOP — это свайп (его ведёт paginator), долгое нажатие —
// выделение текста.
const TAP_SLOP = 10
const TAP_MAX_MS = 500
const TAP_ZONE = 0.3
const activePointers = new Set()
let tap = null
let lastAnnotationPost = { cfi: '', at: 0 }

const postAnnotationClick = (cfi) => {
  const now = performance.now()
  // foliate шлёт show-annotation ещё и по click — второй раз не дублируем.
  if (lastAnnotationPost.cfi === cfi && now - lastAnnotationPost.at < 800) return
  lastAnnotationPost = { cfi, at: now }
  post({ type: 'annotation-click', cfi })
}

const onPointerDown = (e, doc) => {
  if (e.isPrimary) activePointers.clear()
  activePointers.add(e.pointerId)
  if (activePointers.size > 1 || (e.pointerType === 'mouse' && e.button !== 0)) {
    tap = null
    return
  }
  const sel = doc?.getSelection?.()
  tap = {
    id: e.pointerId,
    x: e.screenX, y: e.screenY,
    at: performance.now(),
    // Тап, снимающий выделение, страницу не листает.
    hadSelection: Boolean(sel && !sel.isCollapsed),
  }
}

const onPointerCancel = (e) => {
  activePointers.delete(e.pointerId)
  tap = null
}

// frameLeft — левый край iframe страницы в координатах этого документа
// (страница — широкий iframe со всеми колонками, сдвинутый скроллом).
const onPointerUp = (e, doc, frameLeft) => {
  activePointers.delete(e.pointerId)
  const t = tap
  tap = null
  if (!view || !t || t.id !== e.pointerId) return
  if (Math.hypot(e.screenX - t.x, e.screenY - t.y) > TAP_SLOP) return
  if (performance.now() - t.at > TAP_MAX_MS) return
  if (t.hadSelection) return
  // Ссылки и сноски — foliate (переход по click).
  if (e.target?.closest?.('a[href]')) return
  const sel = doc?.getSelection?.()
  if (sel && !sel.isCollapsed) return
  if (doc) {
    const contents = view.renderer?.getContents?.() ?? []
    const overlayer = contents.find(c => c.doc === doc)?.overlayer
    const [value] = overlayer?.hitTest?.(e) ?? []
    if (value) {
      postAnnotationClick(value)
      return
    }
  }
  const rect = view.getBoundingClientRect()
  const x = (frameLeft ?? 0) + e.clientX
  const f = rect.width ? (x - rect.left) / rect.width : 0.5
  if (settings.tap && f < TAP_ZONE) turn('left')
  else if (settings.tap && f > 1 - TAP_ZONE) turn('right')
  else post({ type: 'toggle-ui' })
}

// Тапы по полям страницы (вне iframe главы) приходят в этот документ.
host.addEventListener('pointerdown', e => onPointerDown(e, null))
host.addEventListener('pointerup', e => onPointerUp(e, null, 0))
host.addEventListener('pointercancel', onPointerCancel)

;(async () => {
  let blob
  try {
    const r = await fetch(src, { credentials: 'same-origin' })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    blob = await r.blob()
  } catch (e) {
    status.textContent = `Не удалось загрузить книгу: ${e.message}`
    post({ type: 'error', reason: 'fetch', detail: String(e) })
    return
  }

  // makeBook принимает File или Blob с MIME-типом epub+zip.
  const file = new File([blob], 'book.epub', { type: 'application/epub+zip' })

  view = document.createElement('foliate-view')
  host.appendChild(view)

  view.addEventListener('load', () => {
    status.remove()
    post({ type: 'ready' })
  })

  // Страница главы (свой документ): выделение текста → родителю (кнопки
  // «Выделить» / «Заметка»), тапы, клавиши.
  view.addEventListener('load', (e) => {
    const { doc, index } = e.detail || {}
    if (!doc) return
    const report = () => setTimeout(() => {
      const sel = doc.getSelection()
      if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
        if (selectionDoc === doc) {
          selectionDoc = null
          post({ type: 'selection-clear' })
        }
        return
      }
      const text = sel.toString().trim()
      if (!text) return
      selectionDoc = doc
      post({ type: 'selection', cfi: view.getCFI(index, sel.getRangeAt(0)), text: text.slice(0, 2000) })
    }, 0)
    doc.addEventListener('pointerup', report)
    doc.addEventListener('keyup', report)
    doc.addEventListener('touchend', report)

    const frameLeft = () => doc.defaultView?.frameElement?.getBoundingClientRect().left ?? 0
    doc.addEventListener('pointerdown', ev => onPointerDown(ev, doc))
    doc.addEventListener('pointerup', ev => onPointerUp(ev, doc, frameLeft()))
    doc.addEventListener('pointercancel', onPointerCancel)
    doc.addEventListener('keydown', onKeyDown)
  })
  // Подсветка выделений и клик по ней.
  view.addEventListener('draw-annotation', (e) => {
    const { draw } = e.detail
    draw(Overlayer.highlight, { color: '#f5c518' })
  })
  view.addEventListener('create-overlay', () => {
    for (const cfi of highlights) view.addAnnotation({ value: cfi }).catch(() => {})
  })
  view.addEventListener('show-annotation', (e) => {
    const { value } = e.detail || {}
    if (value) postAnnotationClick(value)
  })

  view.addEventListener('relocate', (e) => {
    const detail = e.detail || {}
    const cfi = detail.cfi || ''
    const fraction = typeof detail.fraction === 'number' ? detail.fraction : null
    if (cfi && cfi !== lastCfi) {
      lastCfi = cfi
      post({ type: 'position', cfi, fraction, label: detail.tocItem?.label || '', tocId: detail.tocItem?.id ?? null })
    }
  })

  try {
    await view.open(file)
    // Причина перемещения есть только у события renderer'а: page — тап,
    // клавиша; snap — свайп. Это перелистывания пользователем.
    // Обработчик view (relocate → lastLocation) зарегистрирован раньше этого,
    // в view.open, — lastLocation здесь уже про это перемещение.
    view.renderer.addEventListener('relocate', (e) => {
      const reason = e.detail?.reason
      if (reason !== 'page' && reason !== 'snap') return
      post({ type: 'turned' })
      // Дочитывание — только перелистыванием (не прыжком к концу). Fraction
      // может быть 1.0 на последней секции, но во многих epub'ах последний
      // разворот не достигается из-за оглавления/выходных данных — 0.99.
      const loc = view.lastLocation
      if (!completedReported && typeof loc?.fraction === 'number' && loc.fraction >= 0.99) {
        completedReported = true
        post({ type: 'completed', cfi: loc.cfi || '' })
      }
    })
    applyRenderer(settings)
    // ВАЖНО: view.open() только инициализирует renderer, но НЕ загружает
    // первую секцию — без этого 'load' event никогда не fire'ит, и
    // status-loader зависает навсегда. Канонический паттерн в
    // reader.js foliate-js: после open() — либо goTo(cfi) для
    // восстановления позиции, либо renderer.next() для свежего открытия.
    if (initialCfi) {
      await view.goTo(initialCfi)
    } else {
      await view.renderer.next()
    }
    opened = true
    if (seekTarget !== null) seek(seekTarget)
    post({ type: 'toc', items: flattenToc(view.book.toc) })
    const pending = highlights
    highlights = new Set()
    applyHighlights(pending)
  } catch (e) {
    console.error('foliate-reader: open failed', e)
    status.textContent = `Не удалось открыть epub: ${e.message}`
    post({ type: 'error', reason: 'open', detail: String(e) })
  }
})().catch((e) => {
  // Защитный catch на случай если что-то выкинулось ВНЕ try/catch
  // (например, при создании File или парсинге blob'а). Без него
  // окно остаётся в «Загружаем…» и в консоли висит uncaught promise.
  console.error('foliate-reader: top-level error', e)
  if (status && status.isConnected) {
    status.textContent = `Внутренняя ошибка ридера: ${e?.message ?? e}`
  }
  post({ type: 'error', reason: 'uncaught', detail: String(e) })
})
