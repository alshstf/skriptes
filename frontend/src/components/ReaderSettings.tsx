import type { CSSProperties } from 'react';
import { AArrowDown, AArrowUp } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import {
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  FONT_SIZE_STEP,
  READER_MARGINS,
  READER_THEMES,
  type ReaderMargins,
  type ReaderSettings,
  type ReaderTheme,
} from '@/lib/readerSettings';

// Настройки веб-ридера: тема страницы, шрифт, поля, способы перелистывания.
// Применяются сразу (без «Сохранить»), хранятся на этом устройстве. Шторка
// снизу с лёгким затемнением — страницу видно, пока меняешь.

const THEME_OPTIONS: { value: ReaderTheme; label: string }[] = [
  { value: 'auto', label: 'Авто' },
  { value: 'light', label: READER_THEMES.light.label },
  { value: 'sepia', label: READER_THEMES.sepia.label },
  { value: 'dark', label: READER_THEMES.dark.label },
  { value: 'black', label: READER_THEMES.black.label },
];

function swatchStyle(theme: ReaderTheme): CSSProperties {
  if (theme === 'auto') {
    const { light, dark } = READER_THEMES;
    return {
      background: `linear-gradient(135deg, ${light.bg} 50%, ${dark.bg} 50%)`,
      color: '#808080',
    };
  }
  const t = READER_THEMES[theme];
  return { background: t.bg, color: t.fg };
}

export function ReaderSettingsSheet({
  open,
  onOpenChange,
  settings,
  onChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  settings: ReaderSettings;
  onChange: (patch: Partial<ReaderSettings>) => void;
}) {
  // Один способ листать остаётся всегда — иначе страницу не перелистнуть.
  const swipeLocked = settings.swipe && !settings.tapZones;
  const tapLocked = settings.tapZones && !settings.swipe;
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        overlayClassName="bg-black/10"
        className="max-h-[85dvh] gap-0 overflow-y-auto p-0 sm:mx-auto sm:max-w-md sm:rounded-t-xl sm:border-x"
      >
        <SheetHeader className="pb-2">
          <SheetTitle>Настройки чтения</SheetTitle>
          <SheetDescription>Действуют на этом устройстве.</SheetDescription>
        </SheetHeader>
        <div className="space-y-5 px-4 pb-5">
          <section className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">Тема</h3>
            <div className="grid grid-cols-5 gap-2">
              {THEME_OPTIONS.map((o) => {
                const selected = settings.theme === o.value;
                return (
                  <button
                    key={o.value}
                    type="button"
                    aria-pressed={selected}
                    onClick={() => onChange({ theme: o.value })}
                    className="flex flex-col items-center gap-1.5 rounded-md py-1 text-xs focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                  >
                    <span
                      aria-hidden
                      style={swatchStyle(o.value)}
                      className={cn(
                        'flex size-10 items-center justify-center rounded-full border border-border font-serif text-sm',
                        selected && 'ring-2 ring-primary ring-offset-2 ring-offset-background',
                      )}
                    >
                      Aa
                    </span>
                    <span className={cn(selected ? 'text-foreground' : 'text-muted-foreground')}>{o.label}</span>
                  </button>
                );
              })}
            </div>
            {settings.theme === 'auto' ? (
              <p className="text-xs text-pretty text-muted-foreground">
                Светлая или тёмная — как системная тема устройства.
              </p>
            ) : null}
          </section>

          <section className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">Размер шрифта</h3>
            <div className="flex items-center gap-3">
              <Button
                variant="outline"
                size="icon"
                aria-label="Шрифт мельче"
                disabled={settings.fontSize <= FONT_SIZE_MIN}
                onClick={() => onChange({ fontSize: settings.fontSize - FONT_SIZE_STEP })}
              >
                <AArrowDown className="size-4" aria-hidden />
              </Button>
              <output aria-live="polite" className="min-w-14 text-center text-sm tabular-nums">
                {settings.fontSize} %
              </output>
              <Button
                variant="outline"
                size="icon"
                aria-label="Шрифт крупнее"
                disabled={settings.fontSize >= FONT_SIZE_MAX}
                onClick={() => onChange({ fontSize: settings.fontSize + FONT_SIZE_STEP })}
              >
                <AArrowUp className="size-4" aria-hidden />
              </Button>
            </div>
          </section>

          <section className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">Поля</h3>
            <div className="grid grid-cols-3 gap-1 rounded-lg border border-border p-1">
              {(Object.keys(READER_MARGINS) as ReaderMargins[]).map((m) => {
                const selected = settings.margins === m;
                return (
                  <button
                    key={m}
                    type="button"
                    aria-pressed={selected}
                    onClick={() => onChange({ margins: m })}
                    className={cn(
                      'rounded-md px-2 py-1.5 text-sm transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none',
                      selected ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50',
                    )}
                  >
                    {READER_MARGINS[m].label}
                  </button>
                );
              })}
            </div>
          </section>

          <section className="space-y-3">
            <h3 className="text-xs font-medium text-muted-foreground">Перелистывание</h3>
            <SwitchRow
              id="reader-swipe"
              label="Свайпом"
              hint="Страница тянется за пальцем."
              checked={settings.swipe}
              disabled={swipeLocked}
              onChange={(v) => onChange({ swipe: v })}
            />
            <SwitchRow
              id="reader-tap"
              label="Тапом по краям"
              hint="Левый край — назад, правый — вперёд. Тап по центру — меню."
              checked={settings.tapZones}
              disabled={tapLocked}
              onChange={(v) => onChange({ tapZones: v })}
            />
            <SwitchRow
              id="reader-animation"
              label="Анимация"
              hint="Страница плавно уезжает; без неё — меняется мгновенно."
              checked={settings.animation}
              onChange={(v) => onChange({ animation: v })}
            />
            {swipeLocked || tapLocked ? (
              <p className="text-xs text-pretty text-muted-foreground">
                Один способ перелистывания остаётся всегда — иначе страницу не перевернуть.
              </p>
            ) : null}
          </section>
        </div>
      </SheetContent>
    </Sheet>
  );
}

function SwitchRow({
  id,
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <label htmlFor={id} className="min-w-0 space-y-0.5">
        <span className="block text-sm">{label}</span>
        <span className="block text-xs text-pretty text-muted-foreground">{hint}</span>
      </label>
      <Switch id={id} checked={checked} disabled={disabled} onCheckedChange={onChange} />
    </div>
  );
}
