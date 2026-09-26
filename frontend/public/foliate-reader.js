// Логика ридера (iframe /foliate-reader.html). Отдельным файлом, а не inline
// <script>: CSP сайта (frontend/nginx-security-headers.conf) — script-src 'self'
// без 'unsafe-inline'. Страницы книги foliate рендерит в blob:-iframe'ах, они
// наследуют эту CSP — поэтому скрипты внутри книги не выполняются.
//
// Не часть react-bundle'а — статический ассет в public/. Логика:
//
//  1. Читаем ?src=URL&cfi=POS из URL iframe'а.
//  2. Скачиваем epub из src (как blob), парсим через foliate-js makeBook.
//  3. Открываем в <foliate-view>, при наличии cfi восстанавливаем позицию.
//  4. Слушаем relocate event → отправляем postMessage родителю
//     {type:'position', cfi, fraction}. Родитель debounce'нуто PUT'ит
//     в /api/books/{id}/position. При достижении конца (fraction >= 0.995)
//     отправляем {type:'completed'} — родитель вызывает MarkRead.
//  5. Кнопки prev/next из топбара дёргают view.next() / view.prev().
//     На мобиле работает swipe (foliate-paginator делает сам).

import './foliate/view.js'

const params = new URLSearchParams(location.search)
const src = params.get('src')
const initialCfi = params.get('cfi') || ''

const post = msg => parent.postMessage(msg, location.origin)

if (!src) {
  document.getElementById('status').textContent = 'Не задан параметр ?src'
  post({ type: 'error', reason: 'no-src' })
  throw new Error('foliate-reader: no src')
}

const status = document.getElementById('status')
const host = document.getElementById('view-host')
const prevBtn = document.getElementById('prev')
const nextBtn = document.getElementById('next')
const progressEl = document.getElementById('progress')

let view = null
let lastCfi = ''
let completedReported = false

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
    prevBtn.disabled = false
    nextBtn.disabled = false
    post({ type: 'ready' })
  })

  view.addEventListener('relocate', (e) => {
    const detail = e.detail || {}
    const cfi = detail.cfi || ''
    const fraction = typeof detail.fraction === 'number' ? detail.fraction : null
    if (cfi && cfi !== lastCfi) {
      lastCfi = cfi
      post({ type: 'position', cfi, fraction })
    }
    if (fraction !== null) {
      progressEl.textContent = `${Math.round(fraction * 100)}%`
    }
    // Дочитывание: foliate fraction может быть 1.0 на последней
    // секции, но во многих epub'ах последний "spread" вообще не
    // достигается из-за оглавления/выходных данных. 0.99 — компромисс.
    if (!completedReported && fraction !== null && fraction >= 0.99) {
      completedReported = true
      post({ type: 'completed', cfi })
    }
  })

  prevBtn.addEventListener('click', () => view.prev())
  nextBtn.addEventListener('click', () => view.next())

  try {
    await view.open(file)
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
