# API HTTP

Referencia rápida de los endpoints de la API del club. El contrato completo, con cada campo
y cada error, está en [`backend/openapi/openapi.yaml`](../backend/openapi/openapi.yaml); este
fichero es el resumen para quien solo quiere saber qué hay y cómo llamarlo. Cómo se guardan los
datos está en [`database.md`](database.md) y las reglas de negocio, en
[`architecture.md`](architecture.md).

Contenido:

- [Convenciones](#convenciones)
- [Sistema y sesión](#sistema-y-sesión)
- [Personas](#personas)
- [Roles](#roles)
- [Sesiones y suplantación](#sesiones-y-suplantación)
- [Socios](#socios)
- [Actividades](#actividades)
- [Memoria de una actividad](#memoria-de-una-actividad)
- [Material](#material)
- [Repartos](#repartos)
- [Archivo de documentos y ficheros de personas](#archivo-de-documentos-y-ficheros-de-personas)
- [Contabilidad](#contabilidad)
- [Objetos](#objetos)

## Convenciones

- **Base**: todas las rutas cuelgan de `/api/v1`. Los cuerpos y las respuestas son JSON, salvo los
  ficheros (`/library/file/…`, `/users/{login}/files/file/…`, el fichero de un recurso de la
  memoria), que son los bytes, y la creación de un recurso de la memoria, que es
  `multipart/form-data`.
- **Sesión**: no hay contraseñas. `POST /auth/magic-link` manda un enlace con un código; se cambia
  por un token opaco con `POST /auth/magic-link/consume` (o `…/consume-code`) y ese token va en cada
  petición: `Authorization: Bearer <token>`. El token caduca a los 30 días y se puede revocar. Las
  rutas de ficheros (`/library/file/…`, `/users/{login}/files/file/…`) aceptan también la cookie
  `katiuskas_session`, que la API pone al iniciar sesión, para que un documento sea un enlace normal
  del navegador.
- **Rol base**: casi toda ruta con sesión exige además ser `admin`, `board` o `member` (un socio con
  alta activa). Quien solo tenga otro rol, como `material` o `money`, recibe 403 en todas partes. En
  las listas de abajo, «Cualquier rol base» significa justo eso. «La propia persona» es la
  persona dueña del recurso, identificada por el `login` de la ruta.
- **Personas**: se nombran siempre por `login` (por ejemplo `maria.garcia`), nunca por un id
  numérico. El resto de recursos (actividad, licencia, token…) sí tienen un id numérico.
- **Fechas**: un campo «fecha» se envía como `YYYY-MM-DD`, pero la respuesta lo devuelve como
  timestamp RFC 3339 a medianoche UTC (`2024-03-15T00:00:00Z`): léanse solo sus diez primeros
  caracteres. Un campo «fecha y hora» es un timestamp de verdad.
- **Errores**: `{"error": {"code": "…", "message": "…"}}`, con el código HTTP correspondiente. Un
  JSON mal formado es siempre `400 invalid_body` y un fallo inesperado `500 internal_error`; no se
  repiten en cada ruta. Los códigos de abajo salen del OpenAPI y están sacados de él
  automáticamente.
- **Sustitución en `PATCH`**: en casi todos, lo que no se envía se apaga o se vacía (se sustituye
  el registro entero), no se deja como estaba. Lo contrario se dice en la descripción de la ruta.
- **Paginación**: los listados que crecen sin límite aceptan `limit` y `offset`. Un valor fuera de
  rango vuelve al valor por defecto (50 casi siempre) en vez de dar error.
- **Notación**: en los cuerpos, un `*` marca el campo obligatorio.

## Sistema y sesión

Sin contraseñas: se entra con un enlace de un solo uso (o su código de 6 dígitos) que llega por
correo y, si la persona tiene Telegram vinculado, también por Telegram.

- **GET** `/health` — *Público*. Comprueba que el proceso está vivo y llega a PostgreSQL. 200 o 503.
  - devuelve: 200 `JSON`
- **POST** `/auth/magic-link` — *Público*. Pide un enlace de acceso con el correo y el teléfono de
  la persona. La respuesta es siempre la misma, coincida alguien o no. Limitado por dirección de
  origen.
  - cuerpo: `email*`, `phone*`
  - devuelve: 200 `JSON`
  - errores: 400 `invalid_body`, `missing_fields`; 429 `rate_limited`
- **POST** `/auth/magic-link/consume` — *Público*. Cambia el token del enlace por un token de
  sesión.
  - cuerpo: `token*`
  - devuelve: 200 `JSON`
  - errores: 400 `invalid_body`; 401 `invalid_token`
- **POST** `/auth/magic-link/consume-code` — *Público*. Cambia el código de 6 dígitos (más correo y
  teléfono) por un token de sesión. Mismo límite por origen.
  - cuerpo: `email*`, `phone*`, `code*`
  - devuelve: 200 `JSON`
  - errores: 400 `invalid_body`, `missing_fields`; 401 `invalid_code`; 429 `rate_limited`
- **POST** `/auth/logout` — *Público*. Revoca el token de sesión (saber el token es la prueba).
  Responde 204 siempre.
  - cuerpo: `token*`
  - devuelve: 204
  - errores: 400 `invalid_body`
- **GET** `/auth/telegram-bot` — *Público*. Usuario público del bot de Telegram, para ofrecerlo como
  segunda forma de entrar. Vacío si no está configurado.
  - devuelve: 200 `JSON`
- **GET** `/auth/me` — *Cualquier rol base*. Quién es el dueño del token y qué roles tiene ahora.
  Lleva `impersonated_by` en una sesión de suplantación.
  - devuelve: 200 `JSON`
- **POST** `/auth/impersonate/exit` — *Cualquier sesión válida*. Termina una suplantación y devuelve
  un token nuevo para el admin que la empezó.
  - devuelve: 200 `JSON`
  - errores: 409 `not_impersonating`
- **GET** `/admin/status` — *Admin*. Versión, base de datos y migración, correo, Telegram, plazos y
  directorios del proceso en marcha. Nunca incluye secretos.
  - devuelve: 200 `AdminStatus`

## Personas

Una persona se identifica siempre por su `login`, nunca por un id numérico.

- **GET** `/users` — *Cualquier rol base*. Lista de personas. Admin y board reciben la ficha
  completa (`User`); un socio recibe `MemberListing`, solo de quien es o ha sido socio.
  - devuelve: 200 `[User o MemberListing]`
- **POST** `/users` — *Admin*. Crea una persona. El `login` se genera solo y viene en la respuesta.
  - cuerpo: `login`, `first_name*`, `last_name*`, `email`, `national_id`, `birth_date*`, `gender`,
    `phone`, `address`, `postal_code`, `city`, `province`, `emergency_contact`, `role_names`
  - devuelve: 201 `User`
  - errores: 400 `invalid_body`, `invalid_date`, `unknown_role`, `role_names`, `automatic_role`,
    `former_member`; 409 `duplicate`
- **GET** `/users/directory` — *Cualquier rol base*. Solo `login`, nombre y apellidos de todas las
  personas, para elegir a alguien en un selector.
  - devuelve: 200 `[DirectoryEntry]`
- **GET** `/users/{login}` — *La propia persona, admin o board*. Ficha completa de una persona.
  - devuelve: 200 `User`
- **PATCH** `/users/{login}` — *Admin; la propia persona solo sus datos de contacto*. Modifica datos
  personales. Un socio que toque otros campos recibe 403 `restricted_fields`.
  - cuerpo: `login`, `first_name`, `last_name`, `email`, `national_id`, `birth_date`, `gender`,
    `phone`, `address`, `postal_code`, `city`, `province`, `emergency_contact`
  - devuelve: 200 `User`
  - errores: 400 `invalid_body`, `invalid_date`, `invalid_login`; 403 `forbidden`,
    `restricted_fields`; 409 `duplicate`
- **DELETE** `/users/{login}` — *Admin*. Borra a la persona con su historial. No se puede borrar al
  último admin.
  - devuelve: 204
  - errores: 409 `cannot_delete_self`, `has_references`, `last_admin`

## Roles

`member`, `former_member` y `board` son automáticos: se calculan con las altas y los cargos y no se
asignan a mano.

- **GET** `/roles` — *Cualquier rol base*. Catálogo de roles (nombre y descripción), no quién los
  tiene.
  - devuelve: 200 `[Role]`
- **POST** `/roles` — *Admin*. Crea un rol.
  - cuerpo: `name*`, `description`
  - devuelve: 201 `Role`
  - errores: 400 `invalid_body`, `invalid_name`; 409 `duplicate`
- **DELETE** `/roles/{id}` — *Admin*. Borra un rol (los de sistema no se pueden borrar).
  - devuelve: 204
  - errores: 400 `invalid_id`; 409 `system_role`, `former_member`
- **POST** `/users/{login}/roles` — *Admin*. Da un rol a una persona. Rechaza los automáticos.
  - cuerpo: `role_id*`
  - devuelve: 204
  - errores: 400 `invalid_body`; 404 `not_found`; 409 `automatic_role`
- **DELETE** `/users/{login}/roles/{roleID}` — *Admin*. Quita un rol. Un admin no puede quitarse el
  suyo (`self_admin_removal`) ni se puede dejar sin admin al club (`last_admin`).
  - devuelve: 204
  - errores: 400 `invalid_id`; 404 `not_found`; 409 `self_admin_removal`, `last_admin`,
    `automatic_role`

## Sesiones y suplantación

- **GET** `/tokens` — *Admin*. Sesiones de todo el mundo: por defecto las activas; `status=all`
  añade el historial. Marca la propia como `current`.
  - query: `status`, `limit`, `offset`
  - devuelve: 200 `[Token]`
  - errores: 400 `invalid_status`
- **GET** `/users/{login}/tokens` — *Admin*. Todas las sesiones que ha tenido una persona, sin el
  valor del token.
  - devuelve: 200 `[Token]`
- **PATCH** `/users/{login}/tokens/{tokenID}` — *Admin*. Cambia la caducidad de una sesión.
  - cuerpo: `expires_at*`
  - devuelve: 204
  - errores: 400 `invalid_body`
- **DELETE** `/users/{login}/tokens/{tokenID}` — *Admin*. Revoca una sesión; la siguiente petición
  con ella da 401.
  - devuelve: 204
  - errores: 400 `invalid_id`
- **POST** `/users/{login}/impersonate` — *Admin*. Abre una sesión corta (1 hora) que actúa como esa
  persona.
  - devuelve: 201 `JSON`
  - errores: 409 `cannot_impersonate_self`, `not_eligible`

## Socios

Altas y bajas, cuotas anuales, licencias federativas, bajas temporales y Junta Directiva.

- **GET** `/users/{login}/memberships` — *La propia persona, admin o board*. Periodos de alta y baja
  de una persona.
  - devuelve: 200 `[Membership]`
- **POST** `/users/{login}/memberships` — *Admin*. Abre un periodo de socio. Asigna el número de
  socio la primera vez.
  - cuerpo: `start_date*`
  - devuelve: 201 `Membership`
  - errores: 400 `invalid_body`, `invalid_date`; 409 `overlapping_period`
- **GET** `/memberships` — *Admin o board*. Quién es socio hoy.
  - devuelve: 200 `[Membership]`
- **PATCH** `/memberships/{id}` — *Admin*. Corrige las fechas de un periodo o lo cierra.
  - cuerpo: `start_date*`, `end_date`, `end_reason`
  - devuelve: 200 `Membership`
  - errores: 400 `invalid_id`; 409 `overlapping_period`, `outside_membership_period`
- **DELETE** `/memberships/{id}` — *Admin*. Borra un periodo.
  - devuelve: 204
- **GET** `/users/{login}/dues` — *La propia persona, admin o board*. Cuotas anuales registradas,
  con la fecha de pago.
  - devuelve: 200 `[Due]`
- **POST** `/users/{login}/dues` — *Admin*. Registra una cuota anual ligada a la transacción que la
  cobró. Si la persona no tenía ningún alta, también la abre.
  - cuerpo: `year*`, `transaction_id*`, `start_date`
  - devuelve: 201 `Due`
  - errores: 400 `invalid_body`, `missing_fields`, `invalid_date`, `start_date_required`,
    `unknown_transaction`; 409 `due_already_recorded`, `transaction_not_for_dues`
- **GET** `/users/{login}/licenses` — *La propia persona, admin o board*. Licencias federativas de
  una persona.
  - devuelve: 200 `[License]`
- **POST** `/users/{login}/licenses` — *Admin*. Asigna una licencia. Solo a quien fue socio en algún
  momento de su año.
  - cuerpo: `license_type_id*`, `other_detail`, `issued_date*`, `year*`, `transaction_id`
  - devuelve: 201 `License`
  - errores: 400 `invalid_body`, `missing_fields`, `invalid_date`, `invalid_license_type`,
    `invalid_other_detail`, `unknown_transaction`; 409 `not_active_member`,
    `transaction_not_for_licenses`
- **PATCH** `/licenses/{id}` — *Admin*. Corrige una licencia.
  - cuerpo: `license_type_id*`, `other_detail`, `issued_date*`, `year*`
  - devuelve: 200 `License`
  - errores: 400 `invalid_id`, `invalid_body`, `missing_fields`, `invalid_date`,
    `invalid_license_type`, `invalid_other_detail`
- **DELETE** `/licenses/{id}` — *Admin*. Borra una licencia.
  - devuelve: 204
- **GET** `/license-types` — *Admin*. Catálogo de tipos de licencia.
  - devuelve: 200 `[LicenseType]`
- **GET** `/users/{login}/leaves` — *La propia persona, admin o board*. Bajas temporales de una
  persona.
  - devuelve: 200 `[Leave]`
- **POST** `/users/{login}/leaves` — *Admin*. Registra una baja temporal; tiene que caer dentro de
  un periodo de socio.
  - cuerpo: `start_date*`, `end_date`, `reason`
  - devuelve: 201 `Leave`
  - errores: 400 `invalid_body`; 409 `outside_membership_period`, `overlapping_period`
- **PATCH** `/leaves/{id}` — *Admin*. Corrige una baja temporal.
  - cuerpo: `start_date*`, `end_date`, `reason`
  - devuelve: 200 `Leave`
  - errores: 400 `invalid_id`; 409 `outside_membership_period`, `overlapping_period`
- **DELETE** `/leaves/{id}` — *Admin*. Borra una baja temporal.
  - devuelve: 204
- **GET** `/board-members` — *Cualquier rol base*. Junta Directiva actual, con cargo y contacto.
  - devuelve: 200 `[BoardMember]`
- **GET** `/users/{login}/board-members` — *La propia persona, admin o board*. Cargos de una persona
  en la Junta.
  - devuelve: 200 `[BoardMember]`
- **POST** `/users/{login}/board-members` — *Admin*. Abre un cargo.
  - cuerpo: `position*`, `start_date*`
  - devuelve: 201 `BoardMember`
  - errores: 400 `invalid_body`; 409 `overlapping_period`
- **PATCH** `/board-members/{id}` — *Admin*. Corrige el cargo o las fechas, o lo cierra.
  - cuerpo: `position*`, `start_date*`, `end_date`
  - devuelve: 200 `BoardMember`
  - errores: 400 `invalid_id`; 409 `overlapping_period`
- **DELETE** `/board-members/{id}` — *Admin*. Borra un cargo.
  - devuelve: 204

## Actividades

- **GET** `/activities` — *Cualquier rol base*. Lista paginada, la más reciente primero.
  `status=active` deja solo las que aún necesitan atención.
  - query: `status`, `order`, `limit`, `offset`
  - devuelve: 200 `[Activity]`
- **POST** `/activities` — *Cualquier rol base*. Crea una actividad (con participantes iniciales si
  se quiere). El organizador es quien la crea, salvo que admin o board nombren a otro.
  - cuerpo: `title*`, `type`, `start_date*`, `end_date*`, `description`, `visibility`,
    `participant_logins`, `leader_login`
  - devuelve: 201 `Activity`
  - errores: 400 `invalid_body`, `invalid_date`, `invalid_date_range`, `invalid_type`,
    `invalid_visibility`, `unknown_participant`; 403 `forbidden`; 404 `not_found`; 409
    `not_federated`
- **GET** `/activities/{id}` — *Cualquier rol base*. Una actividad.
  - devuelve: 200 `Activity`
  - errores: 400 `invalid_id`
- **PATCH** `/activities/{id}` — *Organizador, admin o board*. Modifica una actividad. Cambiar el
  organizador es solo de admin o board.
  - cuerpo: `title`, `type`, `start_date`, `end_date`, `description`, `status`, `visibility`,
    `leader_login`
  - devuelve: 200 `Activity`
  - errores: 400 `invalid_id`, `invalid_body`, `invalid_date`, `invalid_date_range`, `invalid_type`,
    `invalid_status`, `invalid_visibility`; 403 `forbidden`; 404 `not_found`
- **DELETE** `/activities/{id}` — *Organizador, admin o board*. Cancela una actividad.
  - devuelve: 204
  - errores: 400 `invalid_id`
- **GET** `/activities/{id}/participants` — *Cualquier rol base*. Participantes.
  - devuelve: 200 `[Participant]`
  - errores: 400 `invalid_id`
- **POST** `/activities/{id}/participants` — *Según la visibilidad*. Apuntarse, o apuntar a otra
  persona. En una pública cada uno se apunta; en una privada solo organizador, admin o board. Las de
  espeleo y barranquismo exigen licencia del año (`not_federated`).
  - cuerpo: `login`
  - devuelve: 204
  - errores: 400 `invalid_id`, `invalid_body`; 404 `not_found`; 409 `not_federated`
- **DELETE** `/activities/{id}/participants/{login}` — *Según la visibilidad*. Sale de la actividad,
  o saca a otra persona (admin o board, o el organizador en una privada).
  - devuelve: 204
  - errores: 400 `invalid_id`
- **GET** `/users/{login}/activities` — *Cualquier rol base*. Historial de actividades de una
  persona (como organizador o participante).
  - devuelve: 200 `[Activity]`
- **GET** `/holidays` — *Cualquier rol base*. Festivos del club que marca el calendario.
  - query: `year`
  - devuelve: 200 `[Holiday]`
- **POST** `/holidays` — *Admin o board*. Añade un festivo.
  - cuerpo: `date*`, `name*`
  - devuelve: 201 `Holiday`
  - errores: 400 `invalid_body`; 409 `duplicate_holiday`
- **DELETE** `/holidays/{id}` — *Admin o board*. Borra un festivo.
  - devuelve: 204
  - errores: 400 `invalid_id`

## Memoria de una actividad

La memoria es una lista ordenada de recursos: entradas de texto o ficheros subidos (hasta 25 MiB).

- **GET** `/activities/{id}/resources` — *Cualquier rol base*. Recursos de la memoria, en orden.
  - devuelve: 200 `[ActivityResource]`
  - errores: 400 `invalid_id`
- **POST** `/activities/{id}/resources` — *Admin, board, organizador o participante*. Añade un
  recurso. Siempre `multipart/form-data`, también el de texto.
  - cuerpo: `type*`, `title*`, `description`, `file`
  - devuelve: 201 `ActivityResource`
  - errores: 400 `invalid_id`, `invalid_body`, `invalid_type`, `title_required`,
    `description_required`, `file_required`; 413 `file_too_large`
- **PUT** `/activities/{id}/resources/order` — *Admin, board, organizador o participante*. Sustituye
  el orden entero con la lista completa de ids.
  - cuerpo: `resource_ids*`
  - devuelve: 200 `[ActivityResource]`
  - errores: 400 `invalid_id`, `invalid_body`; 409 `reorder_mismatch`
- **GET** `/activities/{id}/resources/{resourceID}` — *Cualquier rol base*. Un recurso.
  - devuelve: 200 `ActivityResource`
  - errores: 400 `invalid_id`; 404 `not_found`
- **PATCH** `/activities/{id}/resources/{resourceID}` — *Su autor, admin o board*. Cambia título o
  descripción.
  - cuerpo: `title`, `description`
  - devuelve: 200 `ActivityResource`
  - errores: 400 `invalid_id`
- **DELETE** `/activities/{id}/resources/{resourceID}` — *Su autor, admin o board*. Borra el recurso
  y su fichero.
  - devuelve: 204
  - errores: 400 `invalid_id`
- **GET** `/activities/{id}/resources/{resourceID}/file` — *Cualquier rol base*. Bytes del fichero
  de un recurso (`?download=1` para descargarlo).
  - query: `download`
  - errores: 400 `not_a_file`

## Material

- **GET** `/items` — *Cualquier rol base*. Catálogo de material.
  - devuelve: 200 `[Item]`
- **POST** `/items` — *Admin o board*. Da de alta un ítem (cuerda, saca o material genérico).
  - cuerpo: `item_type*`, `brand`, `model`, `code`, `description`, `acquired_at`, `total_quantity`,
    `loanable`, `rental_price_per_week`, `sale_price`, `rope_type`, `diameter_mm`,
    `manufacture_year`, `length_m`, `bag_type`, `capacity_liters`, `category_id`
  - devuelve: 201 `Item`
  - errores: 400 `invalid_body`, `invalid_item_type`, `invalid_date`, `unknown_category`,
    `single_unit_item`, `invalid_item_fields`
- **GET** `/material-categories` — *Admin o board*. Categorías del material genérico.
  - devuelve: 200 `[MaterialCategory]`
- **GET** `/items/{id}/movements` — *Cualquier rol base*. Historial de movimientos de un ítem.
  - devuelve: 200 `[Movement]`
  - errores: 400 `invalid_id`
- **GET** `/movements` — *Admin, board o material*. Movimientos: por defecto los activos, o todos
  los de una actividad con `activity_id`.
  - query: `status`, `activity_id`
  - devuelve: 200 `[Movement]`
  - errores: 400 `invalid_activity_id`
- **POST** `/movements` — *Admin, board o material*. Registra un movimiento (préstamo, alquiler,
  venta, alta, baja, devolución).
  - cuerpo: `item_id*`, `login`, `type*`, `quantity`, `transacted_at`, `activity_id`, `notes`
  - devuelve: 201 `Movement`
  - errores: 400 `invalid_body`, `invalid_date`, `invalid_type`, `login_required`,
    `recipient_not_allowed`, `activity_required`; 404 `not_found`; 409 `insufficient_stock`,
    `not_loanable`, `not_rentable`, `single_unit_item`, `return_exceeds_loaned`
- **PATCH** `/movements/{id}/return` — *Admin, board o material*. Cierra un alquiler abierto.
  - devuelve: 200 `Movement`
  - errores: 400 `invalid_id`; 404 `not_found`
- **GET** `/users/{login}/movements` — *La propia persona, admin, board o material*. Compras,
  alquileres y préstamos de una persona.
  - devuelve: 200 `[Movement]`

## Repartos

Un reparto es algo que el club entrega a sus socios (camisetas, lotería…). Estados: `draft`, `open`,
`closed`, `done`. Pagar y quitar un pago exigen decir en qué cuenta de tipo `asset` se hizo
(`account_id`) y apuntan una transacción en la cuenta del reparto.

- **GET** `/distributions` — *Cualquier rol base*. Repartos, con `eligible` para quien pregunta. Un
  borrador solo lo ven admin y board.
  - devuelve: 200 `[Distribution]`
- **POST** `/distributions` — *Admin o board*. Crea un reparto, en borrador.
  - cuerpo: `name*`, `description`, `audience`, `closes_at`, `cutoff_date`, `account_id`, `price`,
    `extra_price`, `max_extra`, `options`, `articles`, `status`
  - devuelve: 201 `Distribution`
  - errores: 400 `invalid_name`
- **GET** `/distributions/{id}` — *Cualquier rol base*. Un reparto (un borrador es 404 para los
  demás).
  - devuelve: 200 `Distribution`
- **PATCH** `/distributions/{id}` — *Admin o board*. Sustituye todos los campos; también sirve para
  cambiar el estado un paso.
  - cuerpo: `name*`, `description`, `audience`, `closes_at`, `cutoff_date`, `account_id`, `price`,
    `extra_price`, `max_extra`, `options`, `articles`, `status`
  - devuelve: 200 `Distribution`
  - errores: 409 `invalid_status_transition`
- **DELETE** `/distributions/{id}` — *Admin*. Borra el reparto con sus pedidos, salvo que alguno
  esté pagado o entregado.
  - devuelve: 204
  - errores: 409 `has_settled_units`
- **GET** `/distributions/{id}/people` — *`for=order`: cualquier rol base; `for=pickup`: admin,
  board o material*. Socios que ofrecen los selectores de pedir y de entregar.
  - query: `for`
  - devuelve: 200 `[{login, first_name, last_name}]`
- **GET** `/distributions/{id}/units` — *Admin, board o material ven todas; los demás las suyas*.
  Pedidos del reparto.
  - devuelve: 200 `[DistributionUnit]`
- **POST** `/distributions/{id}/units` — *Cualquier rol base*. Pide una unidad (o una cantidad de un
  artículo), para uno mismo o para otro socio.
  - cuerpo: `beneficiary_login`, `is_extra`, `article_id`, `quantity`, `choices`
  - devuelve: 201 `DistributionUnit`
  - errores: 400 `invalid_choices`; 409 `not_open`
- **PATCH** `/distributions/{id}/units/{unitID}` — *Para quien es, quien la apuntó, admin, board o
  material*. Cambia lo elegido o la cantidad.
  - cuerpo: `choices`, `quantity`
  - devuelve: 200 `DistributionUnit`
  - errores: 400 `invalid_choices`, `invalid_quantity`; 409 `not_open`
- **DELETE** `/distributions/{id}/units/{unitID}` — *Para quien es, quien la apuntó, admin, board o
  material*. Quita un pedido (no el automático de un reparto para todos, ni uno con algo pagado).
  - devuelve: 204
  - errores: 409 `not_open`
- **GET** `/distributions/{id}/order` — *Admin, board o material*. Lo que hay que pedir: unidades
  sumadas por combinación de opciones.
  - devuelve: 200 `DistributionOrder`
- **GET** `/distributions/{id}/articles/{articleID}/numbers` — *Admin, board o material*. Todos los
  números de un artículo y quién tiene cada uno.
  - devuelve: 200 `JSON`
  - errores: 400 `article_not_numbered`, `unknown_article`
- **PUT** `/distributions/{id}/units/{unitID}/identifiers` — *Admin, board o material*. Sustituye
  los números de un pedido.
  - cuerpo: `identifiers`
  - devuelve: 200 `DistributionUnit`
  - errores: 400 `invalid_identifiers`; 409 `not_deliverable`, `identifier_taken`
- **POST** `/distributions/{id}/units/{unitID}/identifiers` — *Admin, board o material*. Añade
  números a un pedido.
  - cuerpo: `identifiers`
  - devuelve: 200 `DistributionUnit`
  - errores: 400 `invalid_identifiers`; 409 `not_deliverable`, `identifier_taken`
- **DELETE** `/distributions/{id}/units/{unitID}/identifiers` — *Admin, board o material*. Quita
  números de un pedido.
  - query: `identifiers`*
  - devuelve: 200 `DistributionUnit`
  - errores: 400 `invalid_identifiers`; 409 `not_deliverable`
- **POST** `/distributions/{id}/units/{unitID}/identifiers/assign` — *Admin, board o material*. Da a
  un pedido los siguientes números libres.
  - devuelve: 200 `DistributionUnit`
  - errores: 400 `article_not_numbered`; 409 `not_deliverable`
- **POST** `/distributions/{id}/pickups` — *Admin, board o material*. Apunta entregas, en lote y
  todo o nada.
  - cuerpo: `unit_ids`, `deliveries`, `picked_up_by_login*`, `assign_numbers`
  - devuelve: 204
  - errores: 400 `no_units`; 409 `not_deliverable`
- **DELETE** `/distributions/{id}/units/{unitID}/pickup` — *Admin, board o material*. Deshace una
  entrega.
  - query: `quantity`, `identifiers`
  - devuelve: 204
- **POST** `/distributions/{id}/units/{unitID}/return` — *Admin, board o material*. Devuelve
  unidades entregadas: salen del pedido y sus números quedan libres.
  - cuerpo: `quantity`, `identifiers`
  - devuelve: 204
  - errores: 400 `invalid_quantity`, `invalid_identifiers`; 409 `not_deliverable`,
    `quantity_below_paid`
- **GET** `/distributions/{id}/payments` — *Admin o board*. Lista de pagos del reparto, con el
  acumulado.
  - query: `from`, `to`, `limit`, `offset`
  - devuelve: 200 `[DistributionPayment]`
  - errores: 400 `invalid_date`, `invalid_date_range`
- **POST** `/distributions/{id}/payments` — *Admin o board*. Marca pagos, en lote y todo o nada, y
  los apunta en Contabilidad.
  - cuerpo: `account_id`, `unit_ids`, `payments`
  - devuelve: 204
  - errores: 400 `no_units`; 409 `already_paid`
- **DELETE** `/distributions/{id}/units/{unitID}/payment` — *Admin o board*. Quita un pago, con su
  transacción inversa.
  - query: `account_id`, `quantity`, `identifiers`
  - devuelve: 204
  - errores: 400 `account_required`; 409 `no_distribution_account`

## Archivo de documentos y ficheros de personas

Árboles de directorios en disco, solo lectura: nada de esto está en la base de datos.

- **GET** `/library` — *Cualquier rol base*. Lista un directorio del archivo del club (`?path=`).
  - query: `path`
  - devuelve: 200 `LibraryListing`
  - errores: 400 `invalid_path`, `not_a_directory`
- **GET** `/library/file/{path}` — *Cualquier rol base*. Contenido de un documento. También acepta
  la cookie de sesión, para enlaces normales del navegador.
  - query: `download`
  - errores: 400 `invalid_path`, `not_a_file`
- **GET** `/users/{login}/files` — *La propia persona, admin o board*. Lista un directorio de los
  ficheros de una persona.
  - query: `path`
  - devuelve: 200 `UserFileListing`
  - errores: 400 `invalid_path`, `not_a_directory`
- **GET** `/users/{login}/files/file/{path}` — *La propia persona, admin o board*. Contenido de un
  fichero de una persona (también con la cookie de sesión).
  - query: `download`
  - errores: 400 `invalid_path`, `not_a_file`

## Contabilidad

Plan de cuentas en árbol y transacciones de partida doble. Lectura para cualquier rol base;
escritura de admin o del rol opcional `money`. `GET /transactions` y el libro de una cuenta se
pagina con `limit` (50 por defecto, máximo 100) y `offset`, y se acota con `from` y `to` (días
inclusivos, `YYYY-MM-DD`) y `q` (palabras que tienen que estar todas en la descripción, en cualquier
orden y sin distinguir mayúsculas ni acentos).

- **GET** `/accounts` — *Cualquier rol base*. Plan de cuentas en orden de árbol, con `balance` y
  `last_transaction_at` (en un grupo, los de sus descendientes).
  - devuelve: 200 `[Account]`
- **POST** `/accounts` — *Admin o money*. Crea una cuenta.
  - cuerpo: `name*`, `code`, `parent_id`, `type`, `allows_dues`, `allows_licenses`,
    `distribution_default`, `hidden`
  - devuelve: 201 `Account`
  - errores: 400 `invalid_body`, `missing_fields`, `invalid_type`, `unknown_parent_account`; 409
    `duplicate_code`, `parent_has_entries`
- **GET** `/accounts/{id}` — *Cualquier rol base*. Una cuenta.
  - devuelve: 200 `Account`
  - errores: 400 `invalid_id`
- **PATCH** `/accounts/{id}` — *Admin o money*. Corrige nombre, código, cuenta superior, tipo y
  marcas. Sustituye todos los campos.
  - cuerpo: `name*`, `code`, `parent_id`, `type`, `allows_dues`, `allows_licenses`,
    `distribution_default`, `hidden`
  - devuelve: 200 `Account`
  - errores: 400 `invalid_id`; 409 `duplicate_code`
- **DELETE** `/accounts/{id}` — *Admin o money*. Borra una cuenta sin hijas ni apuntes.
  - devuelve: 204
  - errores: 400 `invalid_id`; 409 `account_in_use`
- **POST** `/accounts/{id}/move` — *Admin o money*. Intercambia el orden con el hermano anterior
  (`up`) o posterior (`down`). Devuelve el plan entero.
  - cuerpo: `direction*`
  - devuelve: 200 `[Account]`
  - errores: 400 `invalid_id`
- **GET** `/accounts/{id}/transactions` — *Cualquier rol base*. Libro de la cuenta, con saldo
  corrido. En una cuenta que agrupa otras, los apuntes de todas sus descendientes.
  - query: `limit`, `offset`, `from`, `to`, `q`
  - devuelve: 200 `[AccountEntry]`
  - errores: 400 `invalid_id`
- **GET** `/transactions` — *Cualquier rol base*. Transacciones, la más reciente primero.
  - query: `limit`, `offset`, `from`, `to`, `q`
  - devuelve: 200 `[Transaction]`
  - errores: 400 `invalid_date`, `invalid_date_range`
- **POST** `/transactions` — *Admin o money*. Registra una transacción: dos o más apuntes que suman
  cero.
  - cuerpo: `description*`, `occurred_at*`, `entries*`
  - devuelve: 201 `Transaction`
  - errores: 400 `invalid_body`; 409 `unbalanced`, `account_has_children`
- **GET** `/transactions/{id}` — *Cualquier rol base*. Una transacción; admin y board ven además las
  cuotas y licencias que pagó.
  - devuelve: 200 `JSON`
  - errores: 400 `invalid_id`
- **PATCH** `/transactions/{id}` — *Admin o money*. Sustituye descripción, fecha y apuntes.
  - cuerpo: `description*`, `occurred_at*`, `entries*`
  - devuelve: 200 `Transaction`
  - errores: 400 `invalid_id`; 409 `unbalanced`
- **DELETE** `/transactions/{id}` — *Admin o money*. Borra la transacción; si era el pago de un
  reparto, lo deshace.
  - devuelve: 204
  - errores: 400 `invalid_id`; 409 `transaction_has_licenses`,
    `transaction_has_distribution_payments`

## Objetos

Campos de los objetos que devuelve o recibe la API (`*` = obligatorio al enviarlo; un campo
sin marca puede faltar o ser nulo). El detalle de cada uno está en `components.schemas` del OpenAPI.

### `Error`

| Campo | Tipo |
|---|---|
| `error` | objeto |

Campos anidados:

- `error`: `{code, message}`

### `AdminStatus`

| Campo | Tipo |
|---|---|
| `app` | objeto |
| `database` | objeto |
| `mail` | objeto |
| `telegram` | objeto |
| `settings` | objeto |
| `storage` | [objeto] |

Campos anidados:

- `app`: `{version, vcs_revision, vcs_time, vcs_modified, go_version, started_at}`
- `database`: `{ok, latency_ms, error, migration_version}`
- `mail`: `{smtp_configured}`
- `telegram`: `{log_channel_configured, bot_running, bot_username}`
- `settings`: `{public_base_url, session_ttl_seconds, magic_link_ttl_seconds,
  magic_link_cooldown_seconds, impersonation_ttl_seconds, bot_dm_link_ttl_seconds}`
- `storage`: `[{name, path, exists}]`

### `Token`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `user_login` | texto |
| `created_at` | fecha y hora |
| `expires_at` | fecha y hora |
| `revoked_at` | fecha y hora |
| `last_used_at` | fecha y hora |
| `last_used_ip` | texto |
| `current` | booleano |
| `impersonated_by_login` | texto |

### `Role`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `name` | texto |
| `description` | texto |
| `is_system` | booleano |
| `is_automatic` | booleano |

### `User`

| Campo | Tipo |
|---|---|
| `login` | texto |
| `first_name` | texto |
| `last_name` | texto |
| `national_id` | texto |
| `birth_date` | fecha |
| `gender` | Gender |
| `email` | texto |
| `phone` | texto |
| `address` | texto |
| `postal_code` | texto |
| `city` | texto |
| `province` | texto |
| `emergency_contact` | texto |
| `roles` | [texto] |
| `last_login_at` | fecha y hora |
| `last_login_ip` | texto |
| `membership_number` | entero |
| `membership_start_date` | fecha |
| `membership_end_date` | fecha |
| `on_leave` | booleano |
| `last_due_year` | entero |
| `last_due_paid_at` | fecha y hora |
| `license_type` | texto |
| `license_year` | entero |
| `license_issued_date` | fecha |
| `created_at` | fecha y hora |

### `MemberListing`

| Campo | Tipo |
|---|---|
| `login` | texto |
| `first_name` | texto |
| `last_name` | texto |
| `gender` | Gender |
| `age_category` | youth/child/junior/senior/veteran_a/veteran_b |
| `membership_number` | entero |
| `membership_start_date` | fecha |
| `membership_end_date` | fecha |
| `on_leave` | booleano |
| `license_type` | texto |
| `license_year` | entero |
| `last_due_year` | entero |

### `DirectoryEntry`

| Campo | Tipo |
|---|---|
| `login` | texto |
| `first_name` | texto |
| `last_name` | texto |

### `CreateUserInput`

| Campo | Tipo |
|---|---|
| `login` | texto |
| `first_name*` | texto |
| `last_name*` | texto |
| `email` | texto |
| `national_id` | texto |
| `birth_date*` | fecha |
| `gender` | objeto |
| `phone` | texto |
| `address` | texto |
| `postal_code` | texto |
| `city` | texto |
| `province` | texto |
| `emergency_contact` | texto |
| `role_names` | [texto] |

### `UpdateUserInput`

| Campo | Tipo |
|---|---|
| `login` | texto |
| `first_name` | texto |
| `last_name` | texto |
| `email` | texto |
| `national_id` | texto |
| `birth_date` | fecha |
| `gender` | objeto |
| `phone` | texto |
| `address` | texto |
| `postal_code` | texto |
| `city` | texto |
| `province` | texto |
| `emergency_contact` | texto |

### `Membership`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `user_login` | texto |
| `first_name` | texto |
| `last_name` | texto |
| `start_date` | fecha |
| `end_date` | fecha |
| `end_reason` | texto |

### `Leave`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `user_login` | texto |
| `start_date` | fecha |
| `end_date` | fecha |
| `reason` | texto |

### `Due`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `user_login` | texto |
| `year` | entero |
| `transaction_id` | entero |
| `created_at` | fecha y hora |
| `paid_at` | fecha y hora |

### `LicenseType`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `name` | texto |
| `is_other` | booleano |

### `License`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `user_login` | texto |
| `license_type_id` | entero |
| `type` | texto |
| `other_detail` | texto |
| `issued_date` | fecha |
| `year` | entero |
| `valid_until` | fecha |
| `transaction_id` | entero |
| `created_at` | fecha y hora |

### `BoardMember`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `user_login` | texto |
| `first_name` | texto |
| `last_name` | texto |
| `email` | texto |
| `phone` | texto |
| `position` | BoardPosition |
| `start_date` | fecha |
| `end_date` | fecha |

### `Activity`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `title` | texto |
| `type` | caving/canyoning/training/social/other |
| `start_date` | fecha |
| `end_date` | fecha |
| `description` | texto |
| `status` | planned/completed/cancelled |
| `leader_login` | texto |
| `visibility` | public/private |

### `CreateActivityInput`

| Campo | Tipo |
|---|---|
| `title*` | texto |
| `type` | caving/canyoning/training/social/other |
| `start_date*` | fecha |
| `end_date*` | fecha |
| `description` | texto |
| `visibility` | public/private |
| `participant_logins` | [texto] |
| `leader_login` | texto |

### `UpdateActivityInput`

| Campo | Tipo |
|---|---|
| `title` | texto |
| `type` | caving/canyoning/training/social/other |
| `start_date` | fecha |
| `end_date` | fecha |
| `description` | texto |
| `status` | planned/completed/cancelled |
| `visibility` | public/private |
| `leader_login` | texto |

### `Participant`

| Campo | Tipo |
|---|---|
| `activity_id` | entero |
| `user_login` | texto |
| `role` | leader/participant |
| `confirmed` | booleano |

### `Distribution`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `name` | texto |
| `description` | texto |
| `audience` | all_members/signup |
| `status` | draft/open/closed/done |
| `closes_at` | fecha y hora |
| `cutoff_date` | fecha |
| `account_id` | entero |
| `account_name` | texto |
| `price` | número |
| `extra_price` | número |
| `max_extra` | entero |
| `options` | [objeto] |
| `articles` | [objeto] |
| `created_by_login` | texto |
| `created_at` | fecha y hora |
| `unit_count` | entero |
| `picked_up_count` | entero |
| `eligible` | booleano |

Campos anidados:

- `options`: `[{name, values}]`
- `articles`: `[{id, name, price, max_total, max_per_person, has_identifiers, identifier_fields,
  identifier_total, identifiers_assigned, requested}]`

### `DistributionInput`

| Campo | Tipo |
|---|---|
| `name*` | texto |
| `description` | texto |
| `audience` | all_members/signup |
| `closes_at` | fecha y hora |
| `cutoff_date` | fecha |
| `account_id` | entero |
| `price` | número |
| `extra_price` | número |
| `max_extra` | entero |
| `options` | [objeto] |
| `articles` | [objeto] |
| `status` | draft/open/closed/done |

Campos anidados:

- `options`: `[{name, values}]`
- `articles`: `[{name, price, max_total, max_per_person, has_identifiers, identifier_fields}]`

### `IdentifierField`

| Campo | Tipo |
|---|---|
| `name*` | texto |
| `spec*` | texto |

### `DistributionUnit`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `distribution_id` | entero |
| `beneficiary_login` | texto |
| `beneficiary_name` | texto |
| `registered_by_login` | texto |
| `registered_at` | fecha y hora |
| `is_extra` | booleano |
| `article_id` | entero |
| `article_name` | texto |
| `identifiers` | [texto] |
| `quantity` | entero |
| `paid_quantity` | entero |
| `choices` | objeto |
| `price` | número |
| `paid_at` | fecha y hora |
| `paid_marked_by_login` | texto |
| `picked_up_quantity` | entero |
| `delivered_identifiers` | [texto] |
| `paid_identifiers` | [texto] |
| `picked_up_at` | fecha y hora |
| `pickup_recorded_by_login` | texto |
| `picked_up_by_login` | texto |

### `DistributionPayment`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `distribution_id` | entero |
| `distribution_name` | texto |
| `unit_id` | entero |
| `transaction_id` | entero |
| `occurred_at` | fecha y hora |
| `recorded_by_login` | texto |
| `recorded_by_name` | texto |
| `paid_by_login` | texto |
| `paid_by_name` | texto |
| `description` | texto |
| `quantity` | entero |
| `amount` | número |
| `identifiers` | [texto] |
| `total_after` | número |

### `DistributionOrder`

| Campo | Tipo |
|---|---|
| `lines` | [objeto] |
| `total_units` | entero |
| `total_amount` | número |
| `provisional` | booleano |

Campos anidados:

- `lines`: `[{article_id, article_name, choices, count}]`

### `Holiday`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `date` | fecha |
| `name` | texto |

### `ActivityResource`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `activity_id` | entero |
| `type` | text/file |
| `author_login` | texto |
| `title` | texto |
| `description` | texto |
| `file_name` | texto |
| `file_size` | entero |
| `content_type` | texto |
| `position` | entero |
| `created_at` | fecha y hora |

### `MaterialCategory`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `name` | texto |

### `Item`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `item_type` | rope/bag/gear |
| `brand` | texto |
| `model` | texto |
| `code` | texto |
| `description` | texto |
| `total_quantity` | entero |
| `available_quantity` | entero |
| `outstanding_loan_quantity` | entero |
| `status` | available/on_loan/rented/sold/lost/abandoned/maintenance/retired |
| `loanable` | booleano |
| `rentable` | booleano |
| `sellable` | booleano |
| `rental_price_per_week` | número |
| `sale_price` | número |
| `rope_type` | caving/canyoning |
| `diameter_mm` | número |
| `manufacture_year` | entero |
| `length_m` | entero |
| `bag_type` | caving/canyoning/quick/special |
| `capacity_liters` | número |
| `category_id` | entero |
| `category_name` | texto |

### `CreateItemInput`

| Campo | Tipo |
|---|---|
| `item_type*` | rope/bag/gear |
| `brand` | texto |
| `model` | texto |
| `code` | texto |
| `description` | texto |
| `acquired_at` | fecha |
| `total_quantity` | entero |
| `loanable` | booleano |
| `rental_price_per_week` | número |
| `sale_price` | número |
| `rope_type` | caving/canyoning |
| `diameter_mm` | número |
| `manufacture_year` | entero |
| `length_m` | entero |
| `bag_type` | caving/canyoning/quick/special |
| `capacity_liters` | número |
| `category_id` | entero |

### `Movement`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `item_id` | entero |
| `user_login` | texto |
| `type` | loan/rental/sale/acquisition/loss/abandonment/retirement/return |
| `quantity` | entero |
| `transacted_at` | fecha |
| `returned_at` | fecha |
| `price` | número |
| `status` | active/completed/overdue/cancelled |
| `recorded_by_login` | texto |
| `activity_id` | entero |
| `notes` | texto |

### `LibraryEntry`

| Campo | Tipo |
|---|---|
| `name` | texto |
| `type` | dir/file |
| `size` | entero |
| `modified_at` | fecha y hora |

### `LibraryListing`

| Campo | Tipo |
|---|---|
| `path` | texto |
| `entries` | [LibraryEntry] |

### `UserFileEntry`

| Campo | Tipo |
|---|---|
| `name` | texto |
| `type` | dir/file |
| `size` | entero |
| `modified_at` | fecha y hora |

### `UserFileListing`

| Campo | Tipo |
|---|---|
| `path` | texto |
| `entries` | [UserFileEntry] |

### `Account`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `name` | texto |
| `code` | texto |
| `parent_id` | entero |
| `type` | asset/income/expense |
| `effective_type` | asset/income/expense |
| `allows_dues` | booleano |
| `allows_licenses` | booleano |
| `distribution_default` | booleano |
| `hidden` | booleano |
| `balance` | número |
| `last_transaction_at` | fecha y hora |

### `AccountEntry`

| Campo | Tipo |
|---|---|
| `transaction_id` | entero |
| `account_id` | entero |
| `account_name` | texto |
| `occurred_at` | fecha y hora |
| `description` | texto |
| `amount` | número |
| `balance_after` | número |

### `Entry`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `account_id` | entero |
| `amount` | número |

### `EntryInput`

| Campo | Tipo |
|---|---|
| `account_id*` | entero |
| `amount*` | número |

### `Transaction`

| Campo | Tipo |
|---|---|
| `id` | entero |
| `description` | texto |
| `occurred_at` | fecha y hora |
| `created_at` | fecha y hora |
| `entries` | [Entry] |

### Enumerados

- **`Gender`**: male / female
- **`BoardPosition`**: president / vice_president / secretary / treasurer / member_at_large
