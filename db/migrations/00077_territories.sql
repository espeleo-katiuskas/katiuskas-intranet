-- +goose Up
-- The territories that ISO 3166-1 has a code for and that are not states of the United Nations
-- (Gibraltar, Bermuda, Greenland, Hong Kong, Puerto Rico…): the geocoder of OpenStreetMap says a
-- point is in one of them by its ISO code, as it does for a country. Each goes below the state that
-- administers it, as a zone of kind "Territorio" whose code is its ISO 3166-1 alpha-2, so that a
-- place can be in it. Left out, because no state administers them or who does is disputed:
-- Antarctica (AQ), Western Sahara (EH) and Taiwan (TW). The names are in Spanish, from CLDR. The
-- state is found by its code, so one that is not in the catalogue adds nothing.
INSERT INTO areas (parent_id, kind, name, norm_name, code)
SELECT s.id, 'Territorio', t.name, t.norm_name, t.code
FROM (VALUES
    ('GB', 'Anguila', 'anguila', 'AI'),
    ('GB', 'Bermudas', 'bermudas', 'BM'),
    ('GB', 'Gibraltar', 'gibraltar', 'GI'),
    ('GB', 'Guernsey', 'guernsey', 'GG'),
    ('GB', 'Isla de Man', 'isla de man', 'IM'),
    ('GB', 'Islas Caimán', 'islas caiman', 'KY'),
    ('GB', 'Islas Georgia del Sur y Sandwich del Sur',
      'islas georgia del sur y sandwich del sur', 'GS'),
    ('GB', 'Islas Malvinas', 'islas malvinas', 'FK'),
    ('GB', 'Islas Pitcairn', 'islas pitcairn', 'PN'),
    ('GB', 'Islas Turcas y Caicos', 'islas turcas y caicos', 'TC'),
    ('GB', 'Islas Vírgenes Británicas', 'islas virgenes britanicas', 'VG'),
    ('GB', 'Jersey', 'jersey', 'JE'),
    ('GB', 'Montserrat', 'montserrat', 'MS'),
    ('GB', 'Santa Elena,
      Ascensión y Tristán de Acuña', 'santa elena ascension y tristan de acuna', 'SH'),
    ('GB', 'Territorio Británico del Océano Índico',
      'territorio britanico del oceano indico', 'IO'),
    ('FR', 'Guadalupe', 'guadalupe', 'GP'),
    ('FR', 'Guayana Francesa', 'guayana francesa', 'GF'),
    ('FR', 'Martinica', 'martinica', 'MQ'),
    ('FR', 'Mayotte', 'mayotte', 'YT'),
    ('FR', 'Nueva Caledonia', 'nueva caledonia', 'NC'),
    ('FR', 'Polinesia Francesa', 'polinesia francesa', 'PF'),
    ('FR', 'Reunión', 'reunion', 'RE'),
    ('FR', 'San Bartolomé', 'san bartolome', 'BL'),
    ('FR', 'San Martín', 'san martin', 'MF'),
    ('FR', 'San Pedro y Miquelón', 'san pedro y miquelon', 'PM'),
    ('FR', 'Territorios Australes Franceses', 'territorios australes franceses', 'TF'),
    ('FR', 'Wallis y Futuna', 'wallis y futuna', 'WF'),
    ('US', 'Guam', 'guam', 'GU'),
    ('US', 'Islas Marianas del Norte', 'islas marianas del norte', 'MP'),
    ('US', 'Islas menores alejadas de EE. UU.', 'islas menores alejadas de ee uu', 'UM'),
    ('US', 'Islas Vírgenes de EE. UU.', 'islas virgenes de ee uu', 'VI'),
    ('US', 'Puerto Rico', 'puerto rico', 'PR'),
    ('US', 'Samoa Americana', 'samoa americana', 'AS'),
    ('NL', 'Aruba', 'aruba', 'AW'),
    ('NL', 'Caribe neerlandés', 'caribe neerlandes', 'BQ'),
    ('NL', 'Curazao', 'curazao', 'CW'),
    ('NL', 'Sint Maarten', 'sint maarten', 'SX'),
    ('DK', 'Groenlandia', 'groenlandia', 'GL'),
    ('DK', 'Islas Feroe', 'islas feroe', 'FO'),
    ('CN', 'Hong Kong', 'hong kong', 'HK'),
    ('CN', 'Macao', 'macao', 'MO'),
    ('AU', 'Isla de Navidad', 'isla de navidad', 'CX'),
    ('AU', 'Isla Norfolk', 'isla norfolk', 'NF'),
    ('AU', 'Islas Cocos', 'islas cocos', 'CC'),
    ('AU', 'Islas Heard y McDonald', 'islas heard y mcdonald', 'HM'),
    ('NZ', 'Islas Cook', 'islas cook', 'CK'),
    ('NZ', 'Niue', 'niue', 'NU'),
    ('NZ', 'Tokelau', 'tokelau', 'TK'),
    ('NO', 'Isla Bouvet', 'isla bouvet', 'BV'),
    ('NO', 'Svalbard y Jan Mayen', 'svalbard y jan mayen', 'SJ'),
    ('FI', 'Islas Åland', 'islas aland', 'AX')
) AS t(state, name, norm_name, code)
JOIN areas s ON s.parent_id IS NULL AND s.code = t.state;

-- +goose Down
DELETE FROM areas
WHERE kind = 'Territorio' AND parent_id IN (SELECT id FROM areas WHERE parent_id IS NULL);
