import { RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { useRegroupWork } from '@/lib/admin';
import { useMe } from '@/lib/auth';

/**
 * RegroupWorkButton — точечный пересбор работы (admin, рядом с «Разделить» в
 * секции «Издания»): издания разбираются в синглтоны с чисткой ошибочных
 * внешних ключей и тут же собираются заново по текущим правилам Tier-1 —
 * one-click починка «в карточку слиплись чужие издания». Подтверждение — с
 * dry-run-прогнозом («пересоберётся в N книг»). Сам скрыт у не-админа.
 */
export function RegroupWorkButton({ workId }: { workId?: number | null }) {
  const { data: me } = useMe();
  const regroup = useRegroupWork();
  if (me?.role !== 'admin' || !workId) return null;

  const onClick = async () => {
    try {
      const dry = await regroup.mutateAsync({ workId, dryRun: true });
      const n = dry.predicted_clusters?.[String(workId)] ?? 1;
      const msg =
        n > 1
          ? `Издания этой работы пересоберутся в ${n} отдельных книги(и) — ` +
            'ошибочно слитые издания отделятся. Продолжить?'
          : 'Прогноз: издания снова соберутся в одну книгу (пересбор освежит ' +
            'связи и очистит ошибочные внешние ключи). Продолжить?';
      if (!window.confirm(msg)) return;
      const res = await regroup.mutateAsync({ workId });
      toast.success(
        res.editions_split > 0
          ? `Пересобрано: вынесено изданий — ${res.editions_split}`
          : 'Пересобрано',
      );
    } catch {
      // тост об ошибке показывает onError хука
    }
  };

  return (
    <Button
      variant="ghost"
      size="sm"
      className="gap-1 text-muted-foreground"
      onClick={onClick}
      disabled={regroup.isPending}
    >
      <RotateCcw className="size-4" aria-hidden />
      {regroup.isPending ? 'Пересбор…' : 'Пересобрать'}
    </Button>
  );
}
