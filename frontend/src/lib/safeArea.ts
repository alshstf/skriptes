// safeArea — фактические safe-area инсеты (px) для Radix-поперов (collisionPadding
// принимает ЧИСЛА, а env() из JS напрямую недоступен и getComputedStyle не всегда
// резолвит env() в custom-property). Меряем скрытым проб-элементом с
// height:env(safe-area-inset-*). На десктопе/в Safari инсеты = 0 → collisionPadding
// не влияет; на iOS PWA — реальные значения, поперы не лезут под бары (грабля №18).

let cache: { top: number; bottom: number } | null = null;

function measure(side: 'top' | 'bottom'): number {
  if (typeof document === 'undefined') return 0;
  const el = document.createElement('div');
  el.style.cssText =
    'position:fixed;left:-9999px;width:0;visibility:hidden;pointer-events:none;' +
    `height:env(safe-area-inset-${side},0px);`;
  document.body.appendChild(el);
  const h = el.getBoundingClientRect().height;
  el.remove();
  return Math.round(h);
}

// safeAreaInsets — измеренные инсеты, кэшируются (стабильны в рамках сессии).
export function safeAreaInsets(): { top: number; bottom: number } {
  if (!cache) cache = { top: measure('top'), bottom: measure('bottom') };
  return cache;
}

// safeCollisionPadding — отступ для Radix `collisionPadding`: safe-area + базовый
// зазор от краёв экрана, чтобы попер-меню не прилипало к статус-бару/индикатору.
export function safeCollisionPadding(): { top: number; bottom: number; left: number; right: number } {
  const { top, bottom } = safeAreaInsets();
  return { top: top + 8, bottom: bottom + 8, left: 8, right: 8 };
}
