-- app_settings — рантайм-настройки приложения, редактируемые из админки
-- (без рестарта). Generic key/value JSONB: один ключ на логический раздел
-- настроек (напр. 'cover_cache'), значение — JSON-объект. Источник правды
-- для рантайм-конфигурируемых параметров; дефолты живут в коде, здесь —
-- только оверрайды, заданные администратором.
CREATE TABLE app_settings (
    key        TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
