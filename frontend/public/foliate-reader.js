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
//     в /api/books/{id}/position. При достижении конца (fraction >= 0.99)
//     отправляем {type:'completed'} — родитель вызывает MarkRead.
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

import './foliate/view.js'
import { Overlayer } from './foliate/overlayer.js'

const params = new URLSearchParams(location.search)
const src = params.get('src')
const initialCfi = params.get('cfi') || ''

const post = msg => parent.postMessage(msg, location.origin)

// Значения по умолчанию — как READER_DEFAULTS (тёмная тема, узкие поля).
const DEFAULT_SETTINGS = {
  bg: '#141414', fg: '#d0d0d0', dark: true,
  fontSize: 100, margin: 24, gap: 4,
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
    case 'goto':
      if (view && msg.cfi) view.goTo(msg.cfi).catch(() => {})
      break
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

// Колонтитулы: сверху — глава, снизу — процент книги (в развороте — под
// правой колонкой). Пересоздаются на каждый render paginator'а, поэтому
// заполняем на каждый relocate.
const updateMarginals = (detail) => {
  const r = view?.renderer
  if (!r) return
  const label = (detail.tocItem?.label || '').trim()
  const pct = typeof detail.fraction === 'number' ? `${Math.round(detail.fraction * 100)}%` : ''
  r.heads?.forEach((el, i) => { el.textContent = i === 0 ? label : '' })
  r.feet?.forEach((el, i, all) => { el.textContent = i === all.length - 1 ? pct : '' })
}

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
      post({ type: 'position', cfi, fraction, label: detail.tocItem?.label || '' })
    }
    updateMarginals(detail)
    // Дочитывание: foliate fraction может быть 1.0 на последней
    // секции, но во многих epub'ах последний "spread" вообще не
    // достигается из-за оглавления/выходных данных. 0.99 — компромисс.
    if (!completedReported && fraction !== null && fraction >= 0.99) {
      completedReported = true
      post({ type: 'completed', cfi })
    }
  })

  try {
    await view.open(file)
    // Причина перемещения есть только у события renderer'а: page — тап,
    // клавиша; snap — свайп. Это перелистывания пользователем.
    view.renderer.addEventListener('relocate', (e) => {
      const reason = e.detail?.reason
      if (reason === 'page' || reason === 'snap') post({ type: 'turned' })
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
