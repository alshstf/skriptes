import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';

// VersionInfo — публичная ручка /api/version: версия Skriptes + версия
// импортированной коллекции (version.info последнего INPX) и время импорта.
export type VersionInfo = {
  version: string;
  instance_name?: string;
  collection_version?: string;
  collection_imported_at?: string;
};

// DEFAULT_INSTANCE_NAME — фолбэк, пока /api/version не загрузился (зеркало
// settings.DefaultInstanceName на бэке).
export const DEFAULT_INSTANCE_NAME = 'Skriptes';

export function useVersion() {
  return useQuery<VersionInfo>({
    queryKey: ['version'],
    queryFn: () => apiFetch<VersionInfo>('/api/version'),
    staleTime: 5 * 60_000,
    retry: false,
  });
}

// useInstanceName — отображаемое имя инстанса (заголовок Главной, <title>).
// Всегда непустое: до загрузки версии — дефолт.
export function useInstanceName(): string {
  const { data } = useVersion();
  return data?.instance_name?.trim() || DEFAULT_INSTANCE_NAME;
}

// formatCollectionVersion — version.info часто YYYYMMDD; показываем как дату,
// иначе как есть.
export function formatCollectionVersion(v: string): string {
  const m = /^(\d{4})(\d{2})(\d{2})$/.exec(v.trim());
  return m ? `${m[1]}-${m[2]}-${m[3]}` : v.trim();
}
