-- +goose Up
-- Ceuta and Melilla are autonomous cities: in law they have no province and no municipality below
-- them. The zones below them (the province 51 and the municipality 51001 of Ceuta, 52 and 52001 of
-- Melilla, which migration 00062 seeded as the INE lists them) are not a level of the hierarchy,
-- which for them is just País (España) > Ciudad autónoma (Ceuta). What pointed at those zones
-- (places, elements of the OpenStreetMap mirror) moves up to the city, and the two levels are
-- deleted. The INE code of the municipality (51001, 52001) is still what OpenStreetMap and
-- Nominatim say, and the code looks the city up instead (areas.AutonomousCityCodes).
UPDATE places SET area_id = city.id
FROM areas city
WHERE city.kind = 'Ciudad autónoma'
  AND places.area_id IN (
    SELECT id FROM areas WHERE parent_id = city.id
    UNION
    SELECT g.id FROM areas g JOIN areas p ON g.parent_id = p.id WHERE p.parent_id = city.id);
UPDATE osm_elements SET area_id = city.id
FROM areas city
WHERE city.kind = 'Ciudad autónoma'
  AND osm_elements.area_id IN (
    SELECT id FROM areas WHERE parent_id = city.id
    UNION
    SELECT g.id FROM areas g JOIN areas p ON g.parent_id = p.id WHERE p.parent_id = city.id);
DELETE FROM areas
WHERE kind = 'Municipio'
  AND parent_id IN (
    SELECT p.id FROM areas p JOIN areas c ON c.id = p.parent_id WHERE c.kind = 'Ciudad autónoma');
DELETE FROM areas
WHERE kind = 'Provincia'
  AND parent_id IN (SELECT id FROM areas WHERE kind = 'Ciudad autónoma');

-- +goose Down
-- The two levels come back as the INE lists them (what pointed at them stays at the city).
INSERT INTO areas (parent_id, kind, name, norm_name, code)
SELECT id, 'Provincia', name, norm_name, CASE code WHEN '18' THEN '51' ELSE '52' END
FROM areas WHERE kind = 'Ciudad autónoma' AND code IN ('18', '19');
INSERT INTO areas (parent_id, kind, name, norm_name, code)
SELECT p.id, 'Municipio', p.name, p.norm_name, CASE p.code WHEN '51' THEN '51001' ELSE '52001' END
FROM areas p JOIN areas c ON c.id = p.parent_id
WHERE p.kind = 'Provincia' AND c.kind = 'Ciudad autónoma';
