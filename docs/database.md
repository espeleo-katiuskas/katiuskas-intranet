# Base de datos

Cómo guarda sus datos la aplicación: PostgreSQL, con el esquema definido en las migraciones de
[goose](https://github.com/pressly/goose) de [`db/migrations/`](../db/migrations/). Este fichero es
el mapa de tablas, columnas y restricciones; las rutas que las usan están en [`api.md`](api.md) y
las reglas de negocio que las rodean, en [`architecture.md`](architecture.md).

Contenido:

- [Convenciones](#convenciones) y [mapa de relaciones](#mapa-de-relaciones)
- [Tipos enumerados](#tipos-enumerados)
- [Personas, acceso y roles](#personas-acceso-y-roles): `users`, `magic_link_tokens`, `tokens`,
  `roles`, `user_roles`
- [Socios](#socios): `memberships`, `membership_leaves`, `membership_dues`, `licenses`,
  `license_types`, `board_members`
- [Actividades](#actividades): `activities`, `activity_participants`, `activity_resources`,
  `holidays`
- [Material](#material): `inventory_items`, `material_categories`, `item_movements`,
  `inventory_item_quantities`
- [Repartos](#repartos): `distributions`, `distribution_options`, `distribution_articles`,
  `distribution_units`, `distribution_unit_identifiers`, `distribution_payments`
- [Contabilidad](#contabilidad): `accounts`, `transactions`, `transaction_entries`
- [Funciones, triggers y extensiones](#funciones-triggers-y-extensiones)
- [Datos sembrados](#datos-sembrados)
- [Migraciones](#migraciones)

## Convenciones

- **Claves**: cada tabla tiene un `id bigint` generado por la base de datos. Ese id **nunca sale por
  la API**: a una persona se la nombra siempre por `users.login`, y el resto de recursos usan su id
  numérico. Las tablas de unión (`user_roles`, `activity_participants`) tienen clave compuesta.
- **Tiempo**: los instantes son `timestamptz`; los días (alta, baja, fecha de actividad), `date`. Un
  periodo de alta o de baja temporal lleva además `period`, una columna `daterange` *generada* a
  partir de `start_date` y `end_date` (cerrada por los dos lados y abierta por el final si no hay
  `end_date`), sobre la que una restricción `EXCLUDE` impide los solapes.
- **Dinero**: `numeric(10,2)`, en euros. En un apunte contable, positivo = entra en la cuenta y
  negativo = sale.
- **Derivados que no se guardan**: la categoría de edad (sale de `birth_date`), el fin de una
  licencia (31 de diciembre de su `year`), los roles automáticos (`member`, `former_member`,
  `board`), las existencias de cada ítem de material (vista `inventory_item_quantities`), el tipo
  efectivo de una cuenta, el saldo y el último movimiento de una cuenta y el saldo acumulado de un
  libro o de la lista de pagos de un reparto.
- **Dónde viven las reglas**: lo que PostgreSQL puede expresar con una restricción (`CHECK`,
  `UNIQUE`, claves foráneas, `EXCLUDE`, triggers) está en la base de datos, y los tests la usan de
  verdad, sin simularla. Lo que depende de varias filas o de otras tablas y no cabe en una
  restricción lo comprueba el servicio de Go, y se indica en cada tabla (apartado «Reglas fuera de
  la base de datos»).
- **Ficheros fuera de PostgreSQL**: el archivo de documentos (`LIBRARY_ROOT`), los ficheros de cada
  persona (`USER_FILES_ROOT`) y los ficheros de la memoria de una actividad
  (`ACTIVITY_RESOURCES_ROOT`) están en disco; en la base de datos solo quedan los metadatos de estos
  últimos (`activity_resources`).

## Mapa de relaciones

```
users ──┬─< memberships, membership_leaves, membership_dues, licenses, board_members
        ├─< user_roles >── roles
        ├─< tokens, magic_link_tokens
        ├─< activities (leader) , activity_participants >── activities
        └─< item_movements (destinatario y quien lo apunta)

activities ──< activity_resources, activity_participants, item_movements
inventory_items ──< item_movements        (inventory_item_quantities es una vista de los dos)
license_types ──< licenses                material_categories ──< inventory_items

distributions ──┬─< distribution_options
                ├─< distribution_articles ──< distribution_unit_identifiers
                ├─< distribution_units ──< distribution_unit_identifiers
                └─< distribution_payments  >── transactions

accounts ──< accounts (parent_id)         accounts ──< transaction_entries >── transactions
accounts ──< distributions (account_id)
transactions ──< membership_dues, licenses, distribution_payments
```

`A ──< B` quiere decir que `B` tiene una clave foránea a `A`. Cada tabla de abajo da el detalle y el
`ON DELETE` de cada una.

## Tipos enumerados

- `account_type` (`accounts.type`): asset, income, expense
- `activity_resource_type` (`activity_resources.type`): text, file
- `activity_status` (`activities.status`): planned, completed, cancelled
- `activity_type` (`activities.type`): caving, canyoning, training, social, other
- `activity_visibility` (`activities.visibility`): public, private
- `bag_type` (`inventory_items.bag_type`): caving, canyoning, quick, special
- `board_position` (`board_members.position`): president, vice_president, secretary, treasurer,
  member_at_large
- `distribution_audience` (`distributions.audience`): all_members, signup
- `distribution_status` (`distributions.status`): draft, open, closed, done
- `gender` (`users.gender`): male, female
- `item_material_type` (`inventory_items.item_type`): rope, bag, gear
- `item_status` (`inventory_items.status`): available, on_loan, rented, sold, lost, abandoned,
  maintenance, retired
- `movement_status` (`item_movements.status`): active, completed, overdue, cancelled
- `movement_type` (`item_movements.type`): loan, rental, sale, acquisition, loss, abandonment,
  retirement, return
- `participant_role` (`activity_participants.role`): leader, participant
- `rope_type` (`inventory_items.rope_type`): caving, canyoning

## Personas, acceso y roles

### `users`

Datos personales de cualquiera vinculado al club. No hay una tabla aparte de cuentas de acceso: lo
que una sesión puede hacer depende solo de los roles de la persona (`user_roles`) y no hay
contraseñas. Sus únicos campos obligatorios al crearla son nombre, apellidos y fecha de nacimiento.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `first_name` | text | no |  |
| `last_name` | text | no |  |
| `national_id` | text | sí |  |
| `birth_date` | date | no |  |
| `gender` | gender | sí |  |
| `email` | text | sí |  |
| `phone` | text | sí |  |
| `address` | text | sí |  |
| `postal_code` | text | sí |  |
| `city` | text | sí |  |
| `province` | text | sí |  |
| `emergency_contact` | text | sí |  |
| `membership_number` | integer | sí |  |
| `created_at` | timestamptz | no | now() |
| `updated_at` | timestamptz | no | now() |
| `last_login_at` | timestamptz | sí |  |
| `last_login_ip` | text | sí |  |
| `login` | text | no |  |
| `telegram_chat_id` | bigint | sí |  |

Notas sobre las columnas:

- `national_id`: DNI. Único si existe.
- `gender`: Opcional. La categoría de edad no se guarda: se calcula a partir de `birth_date`.
- `email`: Uno o varios, separados por espacio. Un correo no puede pertenecer a dos personas (lo
  comprueba el servicio, sin distinguir mayúsculas).
- `phone`: Uno o varios, separados por espacio y sin espacios dentro de cada uno. Un teléfono no
  puede pertenecer a dos personas (el servicio compara solo los dígitos).
- `emergency_contact`: Texto libre; admite varios contactos.
- `membership_number`: Se asigna solo, la primera vez que se da de alta a la persona (el siguiente
  al más alto), y no cambia al darse de baja y volver.
- `last_login_at`: Última vez que inició sesión.
- `last_login_ip`: Dirección de origen (`X-Forwarded-For` si hay proxy delante).
- `login`: Único identificador de la persona hacia fuera: la API nunca expone el `id`. Se genera al
  crear (nombre y apellidos sin acentos, unidos por un punto, con un sufijo numérico si ya existe) y
  solo un admin lo cambia.
- `telegram_chat_id`: Chat de Telegram vinculado. Se borra si cambia el teléfono o si el bot
  descubre que no puede escribir a esa persona.

Restricciones:

- clave primaria (id)
- único (login)
- único (national_id)
- único (telegram_chat_id)
- `membership_number_non_negative`: `membership_number >= 0`

Índices:

- único `idx_users_membership_number` sobre `(membership_number)` cuando `membership_number IS NOT
  NULL`

Reglas fuera de la base de datos:

- Que `email` y `phone` no se repitan entre personas no se puede expresar con un `UNIQUE` (cada
  campo guarda varios valores); lo comprueba `users.Repository` en `Create` y `Update`.

### `magic_link_tokens`

Enlaces de acceso de un solo uso. El mismo intento lleva el hash del token del enlace y el de un
código de 6 dígitos; consumir uno consume el otro. Nunca se guarda el valor en claro.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `user_id` | bigint | no |  |
| `token_hash` | text | no |  |
| `code_hash` | text | sí |  |
| `code_attempts` | integer | no | 0 |
| `expires_at` | timestamptz | no |  |
| `created_at` | timestamptz | no | now() |
| `consumed_at` | timestamptz | sí |  |

Notas sobre las columnas:

- `code_attempts`: Intentos fallidos con el código; pasado un límite (8) deja de aceptarse, sin
  afectar al enlace.
- `consumed_at`: Marca de uso. Consumir el enlace o el código la rellena para los dos.

Restricciones:

- clave primaria (id)
- único (token_hash)
- `user_id` → `users(id)`, se borra con ella

Índices:

- `idx_magic_link_tokens_user_id` sobre `(user_id)`

### `tokens`

Sesiones. El token es opaco: solo se guarda su hash SHA-256, y se puede revocar o cambiar de
caducidad. Una sesión de suplantación lleva en `impersonated_by` el id del admin que la abrió.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `user_id` | bigint | no |  |
| `token_hash` | text | no |  |
| `expires_at` | timestamptz | no |  |
| `created_at` | timestamptz | no | now() |
| `revoked_at` | timestamptz | sí |  |
| `last_used_at` | timestamptz | sí |  |
| `last_used_ip` | text | sí |  |
| `impersonated_by` | bigint | sí |  |

Notas sobre las columnas:

- `last_used_at`: Se actualiza en cada petición con el token.
- `last_used_ip`: Origen de la última petición.

Restricciones:

- clave primaria (id)
- único (token_hash)
- `impersonated_by` → `users(id)`, se borra con ella
- `user_id` → `users(id)`, se borra con ella

Índices:

- `idx_tokens_user_id` sobre `(user_id)`

### `roles`

Catálogo de roles. Los de sistema (`is_system`) son `admin`, `board`, `member` y `former_member`;
los automáticos (`is_automatic`) son `member`, `former_member` y `board`, que nadie asigna a mano.
`material` y `money` están sembrados pero no son de sistema: un club que no los quiera puede
borrarlos. Cualquier otro rol que cree un admin es solo una etiqueta.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `name` | text | no |  |
| `description` | text | sí |  |
| `is_system` | boolean | no | false |
| `is_automatic` | boolean | no | false |
| `created_at` | timestamptz | no | now() |

Restricciones:

- clave primaria (id)
- único (name)

### `user_roles`

Relación muchos a muchos entre personas y roles. Solo guarda los roles asignados a mano: `member`,
`former_member` y `board` se calculan al vuelo a partir de `memberships` y `board_members` y no
aparecen aquí.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `user_id` | bigint | no |  |
| `role_id` | bigint | no |  |

Restricciones:

- clave primaria (user_id, role_id)
- `role_id` → `roles(id)`, se borra con ella
- `user_id` → `users(id)`, se borra con ella

## Socios

### `memberships`

Periodos de alta como socio. Una persona puede tener varios, pero nunca solapados. `period` se
calcula a partir de `start_date` y `end_date` (cerrado por los dos lados; vacío por el final si
sigue de alta).

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `user_id` | bigint | no |  |
| `start_date` | date | no |  |
| `end_date` | date | sí |  |
| `end_reason` | text | sí |  |
| `period` | daterange | sí | generada |

Notas sobre las columnas:

- `end_reason`: Motivo de la baja, si lo hay.

Restricciones:

- clave primaria (id)
- `user_id` → `users(id)`, se borra con ella
- `memberships_user_id_period_excl`: exclusión `(user_id WITH =, period WITH &&)` (nunca dos filas
  de la misma persona con periodos que se solapen)
- `end_not_before_start`: `(end_date IS NULL) OR (end_date >= start_date)`

Índices:

- `idx_memberships_user_id` sobre `(user_id)`

Reglas fuera de la base de datos:

- Se solapan dos periodos de la misma persona si comparten algún día: lo impide la restricción
  `EXCLUDE`, no una comprobación a mano.

### `membership_leaves`

Bajas temporales: una ventana con fecha de inicio y fin opcional (vacía si sigue en curso). Cuelga
de la persona, no de un periodo concreto; que la ventana quede dentro de uno de los periodos de esa
persona lo comprueba el servicio.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `start_date` | date | no |  |
| `end_date` | date | sí |  |
| `reason` | text | sí |  |
| `period` | daterange | sí | generada |
| `user_id` | bigint | no |  |

Restricciones:

- clave primaria (id)
- `user_id` → `users(id)`, se borra con ella
- `membership_leaves_user_id_period_excl`: exclusión `(user_id WITH =, period WITH &&)` (nunca dos
  filas de la misma persona con periodos que se solapen)
- `leave_end_not_before_start`: `(end_date IS NULL) OR (end_date >= start_date)`

Índices:

- `idx_membership_leaves_user_id` sobre `(user_id)`

Reglas fuera de la base de datos:

- Que la baja quede dentro de un periodo de socio de esa persona lo comprueba
  `memberships.Repository` (`findContainingMembership`), porque un `CHECK` no ve otras tablas.

### `membership_dues`

Cuotas anuales de socio: una fila por persona y año cubierto, siempre ligada a la transacción que
registró el pago. A diferencia de una licencia, no tiene sentido sin su transacción, así que se
borra con ella.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `user_id` | bigint | no |  |
| `year` | integer | no |  |
| `transaction_id` | bigint | no |  |
| `created_at` | timestamptz | no | now() |

Restricciones:

- clave primaria (id)
- único (user_id, year)
- `transaction_id` → `transactions(id)`, se borra con ella
- `user_id` → `users(id)`

Índices:

- `idx_membership_dues_user_id` sobre `(user_id)`

Reglas fuera de la base de datos:

- La transacción tiene que tener un apunte en una cuenta `allows_dues`: lo comprueba el servicio al
  registrar la cuota.

### `licenses`

Licencias federativas. El tipo sale del catálogo `license_types`. `year` es el año que cubre (una
licencia pedida en diciembre suele cubrir el siguiente) y es válida hasta el 31 de diciembre de ese
año; esa fecha no se guarda. `transaction_id` es opcional y es la única forma de dejar sin pago una
licencia que el club no ha cobrado.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `user_id` | bigint | no |  |
| `license_type_id` | bigint | no |  |
| `other_detail` | text | sí |  |
| `issued_date` | date | no |  |
| `created_at` | timestamptz | no | now() |
| `year` | integer | no |  |
| `transaction_id` | bigint | sí |  |

Notas sobre las columnas:

- `other_detail`: Obligatorio si el tipo es «Otra» y prohibido en cualquier otro (lo valida el
  servicio).
- `issued_date`: Cuándo se obtuvo; no tiene por qué caer en `year`.

Restricciones:

- clave primaria (id)
- `license_type_id` → `license_types(id)`
- `transaction_id` → `transactions(id)`
- `user_id` → `users(id)`, se borra con ella

Índices:

- `idx_licenses_user_id` sobre `(user_id)`

Reglas fuera de la base de datos:

- Solo se asigna una licencia a quien fue socio en algún momento de su `year`, y si lleva
  `transaction_id` la transacción tiene que tener un apunte en una cuenta `allows_licenses`: ambas
  cosas las comprueba el servicio.

### `license_types`

Catálogo de tipos de licencia, sembrado con las combinaciones de zona (A, B, B1), modalidad (BASICA,
PLUS) y franja de edad (adulto, Infantil, Juvenil), más ZERO, ASEDEB, FAE, FERM, FMM y «Otra». Vive
en la base de datos y no en un enum de Go para poder ampliarlo con un `INSERT`.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `name` | text | no |  |
| `is_other` | boolean | no | false |

Restricciones:

- clave primaria (id)
- único (name)

### `board_members`

Cargos de la Junta Directiva, con la misma forma que `memberships`: una fila por persona y cargo con
fecha de inicio y fin opcional. Varias personas pueden tener el mismo cargo a la vez (hay varios
vocales), pero una sola persona no puede tener dos cargos abiertos al mismo tiempo.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `user_id` | bigint | no |  |
| `position` | board_position | no |  |
| `start_date` | date | no |  |
| `end_date` | date | sí |  |
| `period` | daterange | sí | generada |

Notas sobre las columnas:

- `position`: `president`, `vice_president`, `secretary`, `treasurer` o `member_at_large` (vocal).

Restricciones:

- clave primaria (id)
- `user_id` → `users(id)`, se borra con ella
- `board_members_user_id_period_excl`: exclusión `(user_id WITH =, period WITH &&)` (nunca dos filas
  de la misma persona con periodos que se solapen)
- `board_member_end_not_before_start`: `(end_date IS NULL) OR (end_date >= start_date)`

Índices:

- `idx_board_members_user_id` sobre `(user_id)`

## Actividades

### `activities`

Actividades del club. Toda actividad tiene un organizador (`leader_id`), que por defecto es quien la
crea.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `title` | text | no |  |
| `type` | activity_type | no | 'caving' |
| `start_date` | date | no |  |
| `end_date` | date | no |  |
| `description` | text | sí |  |
| `status` | activity_status | no | 'planned' |
| `leader_id` | bigint | no |  |
| `visibility` | activity_visibility | no | 'public' |
| `created_at` | timestamptz | no | now() |

Notas sobre las columnas:

- `visibility`: `private` limita quién puede apuntar o quitar participantes: solo el organizador,
  admin o board.

Restricciones:

- clave primaria (id)
- `leader_id` → `users(id)`
- `end_not_before_start`: `end_date >= start_date`

Índices:

- `idx_activities_start_date` sobre `(start_date)`

### `activity_participants`

Quién participa en cada actividad. El organizador no tiene por qué constar aquí.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `activity_id` | bigint | no |  |
| `user_id` | bigint | no |  |
| `role` | participant_role | no | 'participant' |
| `confirmed` | boolean | no | true |

Restricciones:

- clave primaria (activity_id, user_id)
- `activity_id` → `activities(id)`, se borra con ella
- `user_id` → `users(id)`, se borra con ella

### `activity_resources`

La memoria de una actividad: lista ordenada de recursos, cada uno una entrada de texto o un fichero
subido. Esta tabla solo guarda los metadatos; los bytes de un fichero viven en disco
(`ACTIVITY_RESOURCES_ROOT`), en un fichero por recurso con el id de la fila como nombre, que se crea
una vez y nunca se modifica.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `activity_id` | bigint | no |  |
| `type` | activity_resource_type | no |  |
| `author_id` | bigint | no |  |
| `title` | text | no |  |
| `description` | text | sí |  |
| `file_name` | text | sí |  |
| `file_size` | bigint | sí |  |
| `content_type` | text | sí |  |
| `position` | integer | no |  |
| `created_at` | timestamptz | no | now() |

Notas sobre las columnas:

- `author_id`: Quién lo escribió; solo él (o admin/board) puede corregirlo o borrarlo.
- `position`: Orden dentro de su actividad, denso de 1 a N.

Restricciones:

- clave primaria (id)
- `activity_id` → `activities(id)`, se borra con ella
- `author_id` → `users(id)`
- `description_only_for_text`: `(type = 'text') OR (description IS NULL)`
- `description_required_for_text`: `(type <> 'text') OR (description IS NOT NULL)`
- `file_fields_only_for_file`: `(type = 'file') OR ((file_name IS NULL) AND (file_size IS NULL) AND
  (content_type IS NULL))`
- `file_fields_required_for_file`: `(type <> 'file') OR ((file_name IS NOT NULL) AND (file_size IS
  NOT NULL) AND (content_type IS NOT NULL))`

Índices:

- `idx_activity_resources_activity_id` sobre `(activity_id)`

### `holidays`

Festivos fijos del club que marca el calendario de actividades. Los fines de semana no están aquí:
se calculan en el cliente. Viene sembrada con los de 2026.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `date` | date | no |  |
| `name` | text | no |  |

Restricciones:

- clave primaria (id)
- único (date)

## Material

### `inventory_items`

Catálogo de material. `item_type` decide qué campos lleva: `rope` (cuerda) y `bag` (saca) son
objetos individuales, cada uno con su código y marca y modelo; `gear` es material genérico,
archivado bajo una categoría y con descripción. Las existencias nunca se guardan: salen de los
movimientos (`inventory_item_quantities`).

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `item_type` | item_material_type | no |  |
| `brand` | text | sí |  |
| `model` | text | sí |  |
| `code` | text | sí |  |
| `description` | text | sí |  |
| `status` | item_status | no | 'available' |
| `loanable` | boolean | no | true |
| `rental_price_per_week` | numeric(10,2) | sí |  |
| `sale_price` | numeric(10,2) | sí |  |
| `rope_type` | rope_type | sí |  |
| `diameter_mm` | numeric(4,1) | sí |  |
| `manufacture_year` | integer | sí |  |
| `length_m` | integer | sí |  |
| `bag_type` | bag_type | sí |  |
| `capacity_liters` | numeric(5,1) | sí |  |
| `category_id` | bigint | sí |  |

Notas sobre las columnas:

- `loanable`: Si se puede prestar a una actividad.
- `rental_price_per_week`: Tarifa de alquiler por semana.

Restricciones:

- clave primaria (id)
- único (code)
- `category_id` → `material_categories(id)`
- `bag_requires_bag_type`: `(item_type <> 'bag') OR (bag_type IS NOT NULL)`
- `bag_requires_capacity`: `(item_type <> 'bag') OR (capacity_liters IS NOT NULL)`
- `bag_type_only_for_bag`: `(item_type = 'bag') OR (bag_type IS NULL)`
- `capacity_only_for_bag`: `(item_type = 'bag') OR (capacity_liters IS NULL)`
- `category_only_for_gear`: `(item_type = 'gear') OR (category_id IS NULL)`
- `gear_requires_category`: `(item_type <> 'gear') OR (category_id IS NOT NULL)`
- `gear_requires_description`: `(item_type <> 'gear') OR (description IS NOT NULL)`
- `rope_and_bag_require_brand_model`: `(item_type = 'gear') OR ((brand IS NOT NULL) AND (model IS
  NOT NULL))`
- `rope_and_bag_require_code`: `(item_type = 'gear') OR (code IS NOT NULL)`
- `rope_fields_only_for_rope`: `(item_type = 'rope') OR ((rope_type IS NULL) AND (diameter_mm IS
  NULL) AND (manufacture_year IS NULL) AND (length_m IS NULL))`
- `rope_requires_rope_fields`: `(item_type <> 'rope') OR ((rope_type IS NOT NULL) AND (diameter_mm
  IS NOT NULL) AND (manufacture_year IS NOT NULL) AND (length_m IS NOT NULL))`

### `material_categories`

Categorías del material genérico (mosquetones, spits, placas, maillones, iluminación). Vive en la
base de datos para poder ampliarla sin desplegar.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `name` | text | no |  |

Restricciones:

- clave primaria (id)
- único (name)

### `item_movements`

Todos los movimientos de material en una sola tabla con discriminador `type`. El alquiler se tarifa
por semana y se cierra de una vez con `returned_at`. Una devolución no se liga a ningún préstamo
concreto: el servicio la valida contra lo que la actividad tiene todavía sin devolver.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `item_id` | bigint | no |  |
| `user_id` | bigint | sí |  |
| `type` | movement_type | no |  |
| `quantity` | integer | no | 1 |
| `transacted_at` | date | no | CURRENT_DATE |
| `returned_at` | date | sí |  |
| `price` | numeric(10,2) | sí |  |
| `status` | movement_status | no | 'active' |
| `recorded_by` | bigint | sí |  |
| `activity_id` | bigint | sí |  |
| `notes` | text | sí |  |

Notas sobre las columnas:

- `type`: `loan` (préstamo a una actividad), `rental` (alquiler), `sale` (venta), `acquisition`
  (entra una existencia), `loss`, `abandonment` y `retirement` (salen sin venta) y `return`
  (devolución de un préstamo).
- `returned_at`: Solo en alquileres, que son los únicos que quedan `active` hasta cerrarse.

Restricciones:

- clave primaria (id)
- `activity_id` → `activities(id)`
- `item_id` → `inventory_items(id)`
- `recorded_by` → `users(id)`
- `user_id` → `users(id)`
- `activity_required_for_loan_and_return`: `(type NOT IN (['loan', 'return'])) OR (activity_id IS
  NOT NULL)`
- `price_only_for_rental_and_sale`: `(type IN (['rental', 'sale'])) OR (price IS NULL)`
- `price_required_for_rental_and_sale`: `(type NOT IN (['rental', 'sale'])) OR (price IS NOT NULL)`
- `recipient_only_for_loan_rental_sale_and_return`: `(type IN (['loan', 'rental', 'sale',
  'return'])) OR (user_id IS NULL)`
- `recipient_required_for_rental`: `(type <> 'rental') OR (user_id IS NOT NULL)`
- `returnable_only_for_rental`: `(type = 'rental') OR (returned_at IS NULL)`

Índices:

- `idx_item_movements_item_id` sobre `(item_id)`
- `idx_item_movements_user_id` sobre `(user_id)`

Reglas fuera de la base de datos:

- Las existencias pendientes de una devolución (préstamos menos devoluciones del mismo ítem y
  actividad) las valida el servicio.

### `inventory_item_quantities`

Vista (no es una tabla): por cada ítem, `total_quantity` (lo que existe), `available_quantity` (lo
disponible ahora) y `outstanding_loan_quantity` (lo prestado y sin devolver), todo recalculado a
partir de `item_movements`.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `item_id` | bigint |  |  |
| `total_quantity` | bigint |  |  |
| `available_quantity` | bigint |  |  |
| `outstanding_loan_quantity` | bigint |  |  |

## Repartos

### `distributions`

Un reparto es algo que el club entrega a sus socios (camisetas, buffs, lotería…). `audience` dice a
quién le toca: `all_members` (todo el que fuera socio en `cutoff_date`) o `signup` (quien se
apunte). `status` avanza un paso cada vez: `draft` → `open` → `closed` → `done`. `account_id` es la
cuenta de ingresos donde se apuntan sus pagos; vacía, se usa la marcada `distribution_default`.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `name` | text | no |  |
| `description` | text | sí |  |
| `audience` | distribution_audience | no | 'all_members' |
| `status` | distribution_status | no | 'draft' |
| `closes_at` | timestamptz | sí |  |
| `cutoff_date` | date | sí |  |
| `price` | numeric(10,2) | no | 0 |
| `extra_price` | numeric(10,2) | sí |  |
| `max_extra` | integer | sí |  |
| `created_by` | bigint | no |  |
| `created_at` | timestamptz | no | now() |
| `account_id` | bigint | sí |  |

Notas sobre las columnas:

- `closes_at`: Fecha límite para que los socios elijan.
- `cutoff_date`: Obligatoria en `all_members`; en `signup`, si se pone, limita a quienes fueran
  socios ese día.
- `extra_price`: Su presencia es lo que hace posibles los extras.
- `account_id`: Cuenta de ingresos de sus pagos; vacía = la marcada `distribution_default`.

Restricciones:

- clave primaria (id)
- `account_id` → `accounts(id)`
- `created_by` → `users(id)`
- `cutoff_date_for_all_members`: `(audience <> 'all_members') OR (cutoff_date IS NOT NULL)`
- `distributions_extra_price_check`: `extra_price >= 0`
- `distributions_max_extra_check`: `max_extra >= 0`
- `distributions_price_check`: `price >= 0`
- `max_extra_needs_extras`: `(max_extra IS NULL) OR (extra_price IS NOT NULL)`

### `distribution_options`

Lo que hay que elegir en un reparto, con sus valores posibles (por ejemplo «Talla»: S, M, L). Solo
se pueden cambiar mientras no haya pedidos.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `distribution_id` | bigint | no |  |
| `name` | text | no |  |
| `values` | text[] | no |  |
| `position` | integer | no |  |

Restricciones:

- clave primaria (id)
- único (distribution_id, name)
- `distribution_id` → `distributions(id)`, se borra con ella
- `distribution_options_values_check`: `cardinality("values") >= 1`

### `distribution_articles`

Artículos de un reparto que se piden en cualquier cantidad, como la lotería (papeletas, décimos).
Cada uno tiene su precio y, si se quiere, un tope total y otro por persona. `identifier_fields`
describe los números de cada unidad (por ejemplo `Serie: 124-133, 137`) como una lista de partes con
sus valores y rangos.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `distribution_id` | bigint | no |  |
| `name` | text | no |  |
| `price` | numeric(10,2) | no | 0 |
| `max_total` | integer | sí |  |
| `max_per_person` | integer | sí |  |
| `has_identifiers` | boolean | no | false |
| `position` | integer | no |  |
| `identifier_fields` | jsonb | no | '[]' |

Notas sobre las columnas:

- `max_total`: Cuántas unidades hay en total. Con números descritos se recalcula siempre como el
  número de combinaciones.
- `has_identifiers`: Cada unidad lleva su número, que se da al entregar y no al pedir.

Restricciones:

- clave primaria (id)
- único (distribution_id, name)
- `distribution_id` → `distributions(id)`, se borra con ella
- `distribution_articles_max_per_person_check`: `max_per_person >= 1`
- `distribution_articles_max_total_check`: `max_total >= 1`
- `distribution_articles_price_check`: `price >= 0`

### `distribution_units`

Los pedidos: una fila por unidad pedida (dos camisetas extra son dos filas, para poder elegir tallas
distintas) o, en un artículo, una fila por persona y artículo con `quantity`. `price` es lo que
costaba UNA unidad al apuntarla y no cambia si luego cambia el del reparto. Pago y entrega se llevan
por filas, y se pueden hacer en parte (`paid_quantity`, `picked_up_quantity`).

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `distribution_id` | bigint | no |  |
| `beneficiary_id` | bigint | no |  |
| `registered_by_id` | bigint | sí |  |
| `registered_at` | timestamptz | no | now() |
| `is_extra` | boolean | no | false |
| `choices` | jsonb | no | '{}' |
| `price` | numeric(10,2) | no |  |
| `paid_at` | timestamptz | sí |  |
| `paid_marked_by_id` | bigint | sí |  |
| `picked_up_at` | timestamptz | sí |  |
| `pickup_recorded_by_id` | bigint | sí |  |
| `picked_up_by_id` | bigint | sí |  |
| `article_id` | bigint | sí |  |
| `quantity` | integer | no | 1 |
| `paid_quantity` | integer | no | 0 |
| `picked_up_quantity` | integer | no | 0 |

Notas sobre las columnas:

- `registered_by_id`: Quién la apuntó de verdad. Vacío en la unidad propia automática de un reparto
  `all_members` que nadie ha elegido todavía.
- `choices`: Lo elegido, como objeto (`{"Talla": "M"}`). Vacío = sin elegir.
- `picked_up_by_id`: A quién se le entregó: no tiene por qué ser el beneficiario.
- `article_id`: Fila de un artículo (con `quantity`); vacío en una unidad normal, donde `quantity`
  es siempre 1.
- `paid_quantity`: Unidades pagadas de las `quantity`. `paid_at` y quién lo marcó son los del último
  pago, y existen exactamente cuando hay algo pagado.
- `picked_up_quantity`: Unidades entregadas. `picked_up_at`, quién lo apuntó y a quién se entregó
  son los de la última entrega.

Restricciones:

- clave primaria (id)
- `article_id` → `distribution_articles(id)`, se borra con ella
- `beneficiary_id` → `users(id)`
- `distribution_id` → `distributions(id)`, se borra con ella
- `paid_marked_by_id` → `users(id)`
- `picked_up_by_id` → `users(id)`
- `pickup_recorded_by_id` → `users(id)`
- `registered_by_id` → `users(id)`
- `article_units_are_plain`: `(article_id IS NULL) OR ((NOT is_extra) AND (choices = '{}'))`
- `distribution_units_price_check`: `price >= 0`
- `distribution_units_quantity_check`: `quantity >= 1`
- `paid_fields_together`: `(paid_at IS NULL) = (paid_marked_by_id IS NULL)`
- `paid_quantity_matches_mark`: `(paid_quantity > 0) = (paid_at IS NOT NULL)`
- `paid_quantity_range`: `(paid_quantity >= 0) AND (paid_quantity <= quantity)`
- `picked_up_fields_together`: `((picked_up_at IS NULL) = (pickup_recorded_by_id IS NULL)) AND
  ((picked_up_at IS NULL) = (picked_up_by_id IS NULL))`
- `picked_up_quantity_matches_mark`: `(picked_up_quantity > 0) = (picked_up_at IS NOT NULL)`
- `picked_up_quantity_range`: `(picked_up_quantity >= 0) AND (picked_up_quantity <= quantity)`
- `quantity_only_for_articles`: `(article_id IS NOT NULL) OR (quantity = 1)`

Índices:

- `idx_distribution_units_article` sobre `(article_id, beneficiary_id)` cuando `article_id IS NOT
  NULL`
- `idx_distribution_units_beneficiary_id` sobre `(beneficiary_id)`
- único `idx_distribution_units_own` sobre `(distribution_id, beneficiary_id)` cuando `(NOT
  is_extra) AND (article_id IS NULL)`

Reglas fuera de la base de datos:

- Quién puede pedir (socio en `cutoff_date`), los topes de artículo y por persona, y el máximo de
  extras se comprueban en el servicio, bloqueando la fila del reparto (`FOR UPDATE`) mientras se
  cuenta.

### `distribution_unit_identifiers`

Un número concreto de un artículo asignado a un pedido (número de serie de una papeleta, serie y
fracción de un décimo), con su estado de pago y de entrega. Un número es único dentro de su
artículo.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `unit_id` | bigint | no |  |
| `article_id` | bigint | no |  |
| `identifier` | text | no |  |
| `delivered_at` | timestamptz | sí |  |
| `paid_at` | timestamptz | sí |  |
| `paid_marked_by_id` | bigint | sí |  |

Restricciones:

- clave primaria (id)
- único (article_id, identifier)
- `article_id` → `distribution_articles(id)`, se borra con ella
- `paid_marked_by_id` → `users(id)`
- `unit_id` → `distribution_units(id)`, se borra con ella
- `distribution_unit_identifiers_identifier_check`: `identifier <> ''`

Índices:

- `idx_distribution_unit_identifiers_unit_id` sobre `(unit_id)`

### `distribution_payments`

Lista de pagos de un reparto: cada pago marcado (o quitado, con cantidad e importe negativos) deja
una entrada. `total_after` de la API se calcula sobre esta tabla con una suma en ventana. Cuando el
pago se hizo con dinero, `transaction_id` es la transacción contable que lo recogió.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `distribution_id` | bigint | no |  |
| `unit_id` | bigint | sí |  |
| `beneficiary_id` | bigint | no |  |
| `description` | text | no |  |
| `quantity` | integer | no |  |
| `amount` | numeric(10,2) | no |  |
| `identifiers` | text[] | no | '{}'[] |
| `occurred_at` | timestamptz | no | now() |
| `recorded_by_id` | bigint | no |  |
| `transaction_id` | bigint | sí |  |

Notas sobre las columnas:

- `unit_id`: El pedido al que se refería; queda vacío si el pedido se borra después.
- `beneficiary_id`: Quien pagó: el socio del pedido (no se guarda un pagador distinto).
- `description`: Qué se pagó: el nombre del artículo, o el del reparto si no tiene artículos.
- `quantity`: Unidades; negativa si se quitó un pago.
- `amount`: Dinero, con el signo de `quantity`.
- `identifiers`: Los números a los que se refería, en un artículo con números.
- `recorded_by_id`: Quién apuntó el pago.
- `transaction_id`: Transacción contable que lo recogió; vacío en los pagos de antes de que
  existiera o si no movían dinero.

Restricciones:

- clave primaria (id)
- `beneficiary_id` → `users(id)`
- `distribution_id` → `distributions(id)`, se borra con ella
- `recorded_by_id` → `users(id)`
- `transaction_id` → `transactions(id)`
- `unit_id` → `distribution_units(id)`, queda vacío si se borra
- `distribution_payments_quantity_check`: `quantity <> 0`

Índices:

- `idx_distribution_payments_distribution` sobre `(distribution_id, occurred_at, id)`
- `idx_distribution_payments_occurred_at` sobre `(occurred_at, id)`
- `idx_distribution_payments_transaction_id` sobre `(transaction_id)`

## Contabilidad

### `accounts`

Plan de cuentas, en árbol. La jerarquía solo agrupa: una cuenta tiene cuentas hijas o tiene
transacciones, nunca las dos cosas. `type` es el tipo propio; el que cuenta (`effective_type` en la
API) es el propio o, si no tiene, el de su cuenta superior, y así hacia arriba (función
`account_effective_type`).

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `name` | text | no |  |
| `code` | text | sí |  |
| `parent_id` | bigint | sí |  |
| `created_at` | timestamptz | no | now() |
| `position` | integer | no |  |
| `allows_dues` | boolean | no | false |
| `allows_licenses` | boolean | no | false |
| `hidden` | boolean | no | false |
| `type` | account_type | sí |  |
| `distribution_default` | boolean | no | false |

Notas sobre las columnas:

- `code`: Opcional, único si existe.
- `position`: Orden manual entre hermanas (mismo `parent_id`); solo sirve para ordenar.
- `allows_dues`: Una transacción con un apunte en esta cuenta puede registrar cuotas anuales. La
  cuenta tiene que ser de tipo `income`.
- `allows_licenses`: Lo mismo para licencias federativas.
- `hidden`: Cuenta sin uso; solo la oculta el frontend.
- `type`: `asset` (dinero que tiene el club), `income` o `expense`. Vacío = hereda el de su cuenta
  superior.
- `distribution_default`: La cuenta donde se apuntan los pagos de los repartos sin cuenta propia.
  Solo una; tiene que ser de tipo `income`.

Restricciones:

- clave primaria (id)
- único (code)
- `parent_id` → `accounts(id)`

Índices:

- único `idx_accounts_distribution_default` sobre `(distribution_default)` cuando
  `distribution_default`
- `idx_accounts_parent_id_position` sobre `(parent_id, position)`

Triggers:

- `accounts_parent_has_no_entries` (ver [Funciones, triggers y
  extensiones](#funciones-triggers-y-extensiones))

Reglas fuera de la base de datos:

- Que un `parent_id` no cree un ciclo lo comprueba el servicio (`circular_parent`).
- Que una cuenta de reparto o de cuotas y licencias siga siendo de tipo `income` tras un cambio de
  tipo, de padre o al crear una nueva lo comprueba el servicio, en la misma transacción.

### `transactions`

Cada movimiento de dinero que registra el club. La transacción solo lleva fecha y descripción; el
dinero está en sus apuntes.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `description` | text | no |  |
| `occurred_at` | timestamptz | no |  |
| `created_at` | timestamptz | no | now() |

Notas sobre las columnas:

- `occurred_at`: Cuándo ocurrió, no cuándo se registró (`created_at`): una cuota de un año pasado
  puede apuntarse mucho después.

Restricciones:

- clave primaria (id)

Índices:

- `idx_transactions_occurred_at` sobre `(occurred_at)`

### `transaction_entries`

Los apuntes de una transacción: una cuenta y un importe, positivo si entra y negativo si sale. Los
de una transacción suman exactamente cero (partida doble) y ninguna cuenta aparece dos veces en la
misma.

| Columna | Tipo | Nulo | Por defecto |
|---|---|---|---|
| `id` | bigint | no | identidad |
| `transaction_id` | bigint | no |  |
| `account_id` | bigint | no |  |
| `amount` | numeric(10,2) | no |  |

Notas sobre las columnas:

- `amount`: Positivo = entra en la cuenta, negativo = sale.

Restricciones:

- clave primaria (id)
- único (transaction_id, account_id)
- `account_id` → `accounts(id)`
- `transaction_id` → `transactions(id)`, se borra con ella
- `amount_not_zero`: `amount <> 0`
- `transaction_entries_balance`: trigger de restricción diferido hasta el `COMMIT`

Índices:

- `idx_transaction_entries_account_id` sobre `(account_id)`
- `idx_transaction_entries_transaction_id` sobre `(transaction_id)`

Triggers:

- `transaction_entries_account_is_leaf` (ver [Funciones, triggers y
  extensiones](#funciones-triggers-y-extensiones))

Reglas fuera de la base de datos:

- Que haya al menos dos apuntes y que sumen cero lo comprueba el trigger de restricción
  `transaction_entries_balance`, diferido hasta el `COMMIT`; los repositorios lo fuerzan inmediato
  dentro de su propia transacción, porque bajo los tests el `COMMIT` real nunca llega.

## Funciones, triggers y extensiones

- **Extensión `btree_gist`** (migración 00001): hace posible la restricción `EXCLUDE USING gist` que
  impide periodos solapados en `memberships`, `membership_leaves` y `board_members`.
- **`check_transaction_balance()`** y el trigger de restricción `transaction_entries_balance` en
  `transaction_entries`: tras escribir los apuntes de una transacción, comprueba que sean al menos
  dos y que sumen exactamente cero (`check_violation`). Es `DEFERRABLE INITIALLY DEFERRED`, así que
  se ejecuta en el `COMMIT` y no fila a fila, y no se queja cuando la transacción entera se está
  borrando.
- **`check_entry_account_is_leaf()`** y el trigger `transaction_entries_account_is_leaf` (migración
  00033): rechaza un apunte en una cuenta que tiene cuentas hijas (`account_has_children`).
- **`check_parent_account_has_no_entries()`** y el trigger `accounts_parent_has_no_entries`
  (migración 00033): rechaza crear una cuenta bajo otra que ya tiene apuntes, o mover una a ella
  (`parent_has_entries`).
- **`account_effective_type(id)`** (migración 00034): el tipo de una cuenta o, si no tiene, el de su
  cuenta superior, y así hacia arriba (nulo si nadie lo tiene). La usan las reglas que dependen del
  tipo y el listado de cuentas.

## Datos sembrados

Las migraciones, además del esquema, dejan estos datos:

- `roles`: `admin`, `board`, `member`, `former_member` (de sistema, los tres últimos automáticos),
  `material` y `money`.
- `license_types`: 24 tipos (zonas A, B y B1 × BASICA y PLUS × adulto, Infantil y Juvenil, más ZERO,
  ASEDEB, FAE, FERM, FMM y «Otra»).
- `material_categories`: Mosquetones, Spits, Placas, Maillones e Iluminación.
- `holidays`: los festivos de 2026.
- Nada más se siembra: ni personas, ni cuentas contables, ni repartos. El primer admin se crea con
  `make seed-admin`.

## Migraciones

Numeradas y aplicadas en orden con goose (`make migrate-up`; la versión aplicada está en
`goose_db_version` y `GET /admin/status` la muestra). Una migración ya publicada no se edita: un
cambio nuevo es otra migración. Los tests de cada paquete que toca PostgreSQL migran una base de
datos de pruebas (`TEST_DATABASE_URL`) y dan a cada test su propia transacción, que se deshace al
terminar; esa base tiene que recrearse cuando aparece una migración nueva.

- `00001_extensions.sql`: extensión `btree_gist`
- `00002_users.sql`: `users`
- `00003_roles.sql`: `roles`, `user_roles`, `tokens` y `magic_link_tokens`
- `00004_memberships.sql`: `memberships`
- `00005_activities.sql`: `activities` y `activity_participants`
- `00006_inventory.sql`: material: `inventory_items`, `item_movements`, vista de existencias
- `00007_licenses.sql`: `license_types` y `licenses`
- `00008_board_members.sql`: `board_members`
- `00009_holidays.sql`: `holidays`
- `00010_activity_resources.sql`: `activity_resources`
- `00011_users_telegram.sql`: `users.telegram_chat_id`
- `00012_accounting.sql`: contabilidad: `accounts`, `transactions`, `transaction_entries` y rol
  `money`
- `00013_accounts_code_optional.sql`: `accounts.code` opcional
- `00014_membership_dues_and_license_payments.sql`: `membership_dues` y año y transacción de
  `licenses`
- `00015_membership_leaves_open_end_date.sql`: bajas temporales con fin opcional
- `00016_membership_leaves_by_user.sql`: bajas temporales por persona, no por periodo
- `00017_accounts_dues_licenses_flags.sql`: `allows_dues` y `allows_licenses` en cuentas
- `00018_transaction_entries_account_once.sql`: una cuenta una sola vez por transacción
- `00019_accounts_hidden.sql`: `accounts.hidden`
- `00020_licenses_transaction_optional.sql`: `licenses.transaction_id` opcional
- `00021_distributions.sql`: repartos: `distributions`, `distribution_options`, `distribution_units`
- `00022_distribution_units_automatic.sql`: unidad propia automática (`registered_by_id` opcional)
- `00023_distribution_status_simplified.sql`: estados de reparto simplificados
- `00024_distribution_articles.sql`: `distribution_articles`
- `00025_distribution_status_no_delivering.sql`: quita el estado `delivering`
- `00026_distribution_article_numbering.sql`: números descritos de un artículo
- `00027_distribution_partial_payments.sql`: pago parcial por filas
- `00028_distribution_partial_pickups.sql`: entrega parcial por filas
- `00029_distribution_item_payments.sql`: pago por número
- `00030_distribution_payments.sql`: `distribution_payments`
- `00031_accounts_type.sql`: `accounts.type`
- `00032_distribution_accounts.sql`: cuentas de los repartos y
  `distribution_payments.transaction_id`
- `00033_accounts_leaf_or_group.sql`: una cuenta agrupa o tiene transacciones, nunca las dos
- `00034_accounts_effective_type.sql`: `account_effective_type`
