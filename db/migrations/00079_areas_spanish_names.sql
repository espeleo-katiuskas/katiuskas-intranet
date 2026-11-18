-- +goose Up
-- The autonomous communities and provinces that migration 00062 named as the INE does, in the
-- language of their own territory or in two ("Araba/Álava", "Illes Balears", "Comunitat
-- Valenciana", "Girona", "A Coruña"), get their Spanish name, since the intranet is in Spanish:
-- "Álava", "Islas Baleares", "Comunidad Valenciana", "Gerona", "La Coruña". The rows are the same
-- (what points at them keeps doing so): only `name` and `norm_name` change. The municipalities
-- keep the name that the INE gives them.
UPDATE areas AS a
SET name = v.name, norm_name = v.norm_name
FROM (VALUES
    ('04', 'Islas Baleares', 'islas baleares'),
    ('10', 'Comunidad Valenciana', 'comunidad valenciana')
) AS v(code, name, norm_name)
WHERE a.kind = 'Comunidad autónoma' AND a.code = v.code
  AND a.parent_id = (SELECT id FROM areas WHERE parent_id IS NULL AND code = 'ES');

UPDATE areas AS a
SET name = v.name, norm_name = v.norm_name
FROM (VALUES
    ('01', 'Álava', 'alava'),
    ('03', 'Alicante', 'alicante'),
    ('07', 'Islas Baleares', 'islas baleares'),
    ('12', 'Castellón', 'castellon'),
    ('15', 'La Coruña', 'la coruna'),
    ('17', 'Gerona', 'gerona'),
    ('20', 'Guipúzcoa', 'guipuzcoa'),
    ('25', 'Lérida', 'lerida'),
    ('32', 'Orense', 'orense'),
    ('46', 'Valencia', 'valencia'),
    ('48', 'Vizcaya', 'vizcaya')
) AS v(code, name, norm_name)
WHERE a.kind = 'Provincia' AND a.code = v.code
  AND a.parent_id IN (
    SELECT c.id FROM areas c JOIN areas s ON s.id = c.parent_id
    WHERE c.kind IN ('Comunidad autónoma', 'Ciudad autónoma')
      AND s.parent_id IS NULL AND s.code = 'ES'
  );

-- +goose Down
UPDATE areas AS a
SET name = v.name, norm_name = v.norm_name
FROM (VALUES
    ('04', 'Illes Balears', 'illes balears'),
    ('10', 'Comunitat Valenciana', 'comunitat valenciana')
) AS v(code, name, norm_name)
WHERE a.kind = 'Comunidad autónoma' AND a.code = v.code
  AND a.parent_id = (SELECT id FROM areas WHERE parent_id IS NULL AND code = 'ES');

UPDATE areas AS a
SET name = v.name, norm_name = v.norm_name
FROM (VALUES
    ('01', 'Araba/Álava', 'araba alava'),
    ('03', 'Alicante/Alacant', 'alicante alacant'),
    ('07', 'Illes Balears', 'illes balears'),
    ('12', 'Castellón/Castelló', 'castellon castello'),
    ('15', 'A Coruña', 'a coruna'),
    ('17', 'Girona', 'girona'),
    ('20', 'Gipuzkoa', 'gipuzkoa'),
    ('25', 'Lleida', 'lleida'),
    ('32', 'Ourense', 'ourense'),
    ('46', 'Valencia/València', 'valencia valencia'),
    ('48', 'Bizkaia', 'bizkaia')
) AS v(code, name, norm_name)
WHERE a.kind = 'Provincia' AND a.code = v.code
  AND a.parent_id IN (
    SELECT c.id FROM areas c JOIN areas s ON s.id = c.parent_id
    WHERE c.kind IN ('Comunidad autónoma', 'Ciudad autónoma')
      AND s.parent_id IS NULL AND s.code = 'ES'
  );
