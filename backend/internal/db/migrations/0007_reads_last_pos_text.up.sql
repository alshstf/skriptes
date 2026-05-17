-- last_pos: INTEGER → TEXT для хранения epub-cfi (стандарт foliate-js).
--
-- В 0001 поле было INTEGER «позиция (epub-cfi или offset)» — компромисс
-- на момент когда реализация ридера ещё не была выбрана. Сейчас в качестве
-- ридера в браузере используется foliate-js, который оперирует epub-cfi
-- — это строка вида "epubcfi(/6/4!/4/2,/1:0,/4/3:120)". В INTEGER не
-- помещается; меняем на TEXT.
--
-- Поскольку колонка ещё нигде не заполнялась (полноценного ридера до
-- этой миграции в проде не было), просто DROP + ADD без миграции данных.

ALTER TABLE reads DROP COLUMN last_pos;
ALTER TABLE reads ADD COLUMN last_pos TEXT;
