# Arquitectura

Documento de referencia, en sincronía con el código.

Contenido:

- [Idioma](#idioma) y [Componentes](#componentes).
- [Permisos](#permisos): qué puede hacer cada nivel de acceso.
- [Modelo de datos](#modelo-de-datos-dbmigrations): una subsección por tabla o recurso,
  con sus reglas y sus rutas.
- [Backend](#backend-backend): capas, sesiones, Telegram.
- [Frontend](#frontend-web): las páginas y su mecánica compartida.
- [Cómo ejecutar en local](#cómo-ejecutar-en-local).

## Idioma

- Código (identificadores, comentarios, logs, mensajes de error de la API): **inglés**.
- Documentación (este fichero, el README): español o inglés indistintamente.
- Frontend web (`web/`): todo el texto visible para la persona usuaria, en **español**.
  Los identificadores del propio código JS/HTML siguen en inglés.

## Componentes

```
web/ (HTML + JS, español)  →  backend/ (API REST en Go, /api/v1)  →  PostgreSQL
```

Un único servicio en Go concentra la lógica de negocio y el acceso a datos. Ningún
cliente toca PostgreSQL directamente, lo que permite añadir una app móvil o un cliente
de terminal más adelante sin duplicar reglas de negocio: comparten el contrato descrito
en `backend/openapi/openapi.yaml`.

## Permisos

Hay tres niveles, de más a menos acceso. Cada ruta aplica el suyo con
`Authenticator.Protect` más, cuando hace falta, una comprobación adicional dentro del
propio handler. Además, `auth.BaselineRoles` exige admin, board o member en todas las
rutas protegidas.

| Nivel | Puede ver | Puede modificar |
|---|---|---|
| `admin` | Todo | Todo, sin excepción |
| `board` | Todos los datos del API | Actividades, material y festivos del calendario |
| `member` (socio) | Una vista reducida de las personas y sus propios datos | Sus propios datos de contacto |

### `admin`

Puede hacer todo, sin excepción.

### `board`

Puede **ver** todos los datos del API: personas, socios, licencias, cuotas, bajas
temporales, Junta Directiva, roles, actividades, material y contabilidad.

Solo puede **modificar**:

- Actividades (cualquiera, no solo las suyas).
- Material (alta de ítems y movimientos).
- Los festivos del calendario.

Todo lo demás es admin-only: no puede crear ni borrar personas, dar de alta/baja,
corregir bajas temporales, registrar cuotas, asignar licencias, gestionar la Junta
Directiva ni asignar/quitar roles a nadie. Las rutas afectadas son las de `/users`,
`/memberships`, `/leaves`, `/licenses`, `/board-members` y
`/users/{login}/{licenses,dues,leaves,memberships,roles}`.

Tampoco puede escribir en contabilidad, que admite a admin y al rol opcional `money`
(ver «Contabilidad»).

### `member` (socio)

**Listado de personas.** Un member puede llamar a `GET /users`, pero no obtiene la ficha
completa de cada persona sino solo `users.MemberListing`, y solo de quien es o ha sido
socio (`membership_start_date` no nulo). `users.Handler.list` decide qué forma devolver
según el rol de quien pregunta; no hay una ruta aparte.

A quien nunca ha tenido un alta (una cuenta solo-admin, por ejemplo) `Handler.list` lo
excluye directamente de este listado para member, aunque admin/board lo sigan viendo.

`MemberListing` contiene:

- Nombre, apellidos y género.
- `age_category`: categoría de edad (ver «`users`», «Género y categoría»).
- Número de socio y fecha de alta y de baja.
- `on_leave`.
- `license_type` y `license_year`: la licencia federativa **del año actual**, si la hay.
  Una de otro año no se muestra, y tener `license_type` es estar federado.
- `last_due_year`: el último año cuya cuota ha pagado.

Quedan reservados a admin/board:

- La fecha de obtención de la licencia.
- Su fecha de fin, que no se muestra porque siempre es el 31 de diciembre.
- `last_due_paid_at`, la fecha en que se pagó la última cuota.

**Categoría de edad.** `age_category` (youth/child/junior/senior/veteran_a/veteran_b:
alevín, infantil, juvenil, mayor, veterano A y veterano B en la normativa del club) se
calcula con `ageCategoryFor` a partir de `birth_date`, según la edad que la persona
cumple en el año natural en curso. Sustituye a la fecha de nacimiento exacta, que no se
expone aquí: el tramo basta para lo que hace falta ver de un vistazo sin revelar la
fecha de nacimiento de nadie.

**Roles y Junta Directiva.** `MemberListing` no lleva un campo `roles`: a un socio
cualquiera no le hace falta saber qué roles internos (`admin`, `money`, `material` o
cualquier etiqueta que haya creado un admin) tiene otra persona. Es información no
pública, igual que el DNI o el contacto, y esta vista reducida existe para no enseñarla.

- La única excepción es el cargo en la Junta, que sí es público dentro del club. Viene de
  `GET /board-members`, abierto también a member, y de ahí `web/js/members.js` saca su
  columna "Cargo" (`cargoFor`, compartida por `FULL_COLUMNS` y `MEMBER_COLUMNS`).
  Quien no tiene cargo actual no muestra nada ahí, nunca un rol.
- `GET /roles` es el catálogo de nombres y descripciones, de solo lectura para cualquiera.
  No dice quién tiene cada rol, solo qué roles existen; asignar o quitar uno sigue siendo
  cosa exclusiva de admin.

**Tipo de socio.** Gracias a `on_leave`, "Tipo de socio" (Activo/Baja temporal/Ex-socio)
se calcula igual para todos. El filtro correspondiente para member no ofrece "Nunca ha
sido socio", porque `GET /users` nunca le manda a un member alguien que nunca haya sido
socio.

**Edición de datos.** Sobre su propio registro, un member puede editar solo datos de
contacto: `email`, `phone`, `address`, `postal_code`, `city`, `province` y
`emergency_contact`. No puede cambiar nombre, apellidos, DNI, fecha de nacimiento ni
género: `users.Handler.update` rechaza con 403 `restricted_fields` cualquier intento de
tocar esos campos si quien llama no es admin, en vez de ignorarlos en silencio.

**Ficha completa.** `GET /users/{login}` queda reservado a admin/board o a la propia
persona.

### Efecto en el frontend

**`/members`.** Es visible para member además de admin/board:

- Las columnas y los filtros se reducen a lo que `MemberListing` realmente trae
  (`MEMBER_COLUMNS`/`MEMBER_FILTERS` en `web/js/members.js`, en vez de las versiones
  `FULL_*`).
- Las filas no son clicables: `/member?login=…` es solo para admin/board.
- Las secciones de gestión (roles, nueva persona) llevan `data-roles="admin"` o
  `"admin,board"` para que `applyRoleVisibility()` las quite del todo.

**`/member`.** Los botones de escritura (dar de alta, licencias, Junta Directiva, gestión
de roles, borrar persona) llevan `data-roles="admin"` en el HTML estático.

Las acciones que se generan dinámicamente por evento del timeline (los botones "Editar"
de cada fila) se filtran en JavaScript comprobando `isRealAdmin`. Es necesario porque
`data-roles` no llega a examinar contenido que todavía no existía cuando
`applyRoleVisibility()` se ejecutó.

## Modelo de datos (`db/migrations/`)

Esquema de PostgreSQL, con migraciones numeradas de
[goose](https://github.com/pressly/goose). Cada subsección describe una tabla (o un grupo
de tablas relacionadas) junto con las reglas de negocio que la rodean y sus rutas de la
API. Las reglas que PostgreSQL no puede expresar con una restricción (porque dependen de
varias filas o de otras tablas) se comprueban en el servicio o el repositorio de Go, y se
indica dónde.

### `users`

Datos personales de cualquier persona vinculada al club. No hay una tabla aparte de
cuentas de acceso ni ningún paso de "conceder acceso" (la tabla `accounts` es el plan
contable, ver «Contabilidad»): lo que puede hacer una sesión depende solo de los roles de
esa persona (ver «`roles` y `user_roles`»), y esos mismos roles deciden si le llega o no
un enlace de acceso. No hay contraseñas.

**Campos de actividad de sesión**

- `last_login_at` y `last_login_ip`: la IP de la última vez que inició sesión,
  `X-Forwarded-For` si existe (en producción hay un proxy inverso por delante) y la propia
  conexión si no.

**Campos obligatorios y opcionales**

- Los únicos campos realmente obligatorios al crear una persona son nombre, apellidos y
  fecha de nacimiento.
- `birth_date` es `NOT NULL` (migración `00002_users.sql`): toda persona necesita una, no
  solo para calcular la categoría.
- `email` es opcional: a quien no tenga ninguno registrado no le llega nunca un enlace de
  acceso, igual que a quien no tiene teléfono.

#### Identidad y login

`login` (`text UNIQUE NOT NULL`, migración `00002_users.sql`) es el único identificador de
una persona que cruza el API: ninguna respuesta ni ruta expone jamás el id numérico
interno.

- **Generación**: automática al crear a la persona. Nombre y apellidos pasan por
  `users.slugify` (acentos fuera, todo lo que no sea letra o dígito colapsado a un guión)
  y se unen con un punto; si esa combinación ya existe se añade un sufijo numérico
  (`users.Repository.generateLogin`).
- **Edición**: solo admin, vía `PATCH /users/{login}`. El formato se comprueba con la misma
  expresión regular (`ErrInvalidLogin` si no cumple; `ErrDuplicate` si ya es de otra
  persona).
- **Otros paquetes**: `tokens`, `roles`, `licenses`, `memberships`, `board`, `inventory` y
  `activities` reciben un login en sus rutas y lo traducen al id interno con una interfaz
  mínima definida en cada uno (`UserLookup`, con `UserIDForLogin`/`LoginForUserID`) que
  `*users.Service` satisface de forma estructural. No pueden importar `users` directamente,
  porque `users` ya importa a varios de ellos (`roles`, `memberships`, `licenses`).
- **Listados mixtos**: un listado que puede mezclar a varias personas a la vez
  (`GET /movements`, `GET /activities`, `GET /transactions`...) resuelve el login con un
  `JOIN` SQL contra `users` en vez de una consulta por fila. Una escritura de una sola fila
  con `INSERT`/`UPDATE ... RETURNING` (que no admite `JOIN`) lo resuelve a partir de un
  login que el handler ya tiene a mano, o con una llamada a `LoginForUserID`.

#### Número de socio

`membership_number` vive en `users`, no en `memberships`: identifica a la persona mientras
esté vinculada al club, no un periodo de alta concreto, así que darse de baja y volver a
darse de alta nunca se lo cambia.

- Es un entero no negativo y único (`CHECK` e índice único, ver
  `db/migrations/00002_users.sql`).
- Se asigna automáticamente, nunca a mano, la primera vez que a alguien se le da de alta:
  `memberships.Service.Create` llama a `users.Service.EnsureMembershipNumber`, que le pone
  el siguiente número tras el más alto que haya en ese momento y no hace nada si la
  persona ya tiene uno.
- `memberships` no puede importar `users` (es al revés), así que expone la función como
  `memberships.MembershipNumberAssigner` y `cmd/api`/`cmd/seed-admin` la conectan tras
  construir ambos servicios.

#### Género y categoría

- `gender` es un tipo enum de PostgreSQL (`male`/`female`), opcional y nulo por defecto:
  nadie lo rellena al crear una persona salvo que se indique explícitamente.
- La categoría (alevín, infantil, juvenil, mayor, veterano A, veterano B) no se guarda: el
  backend la calcula al vuelo (`ageCategoryFor`) a partir de `birth_date` y el año actual
  y la expone como `age_category` en `MemberListing`. `web/js/members.js` solo la traduce
  a su etiqueta.

#### Email y teléfono múltiples

`email` y `phone` pueden llevar más de un valor separados por espacio (igual que
`emergency_contact`, que admite varios contactos en texto libre), en vez de una tabla
aparte: encajar "alguno de estos" es una comprobación de solape de arrays
(`string_to_array(...) && string_to_array(...)`), y esta escala de club no necesita más.

- **Unicidad**: un correo o un teléfono no puede pertenecer a dos personas a la vez, aunque
  cada campo admita varios. Postgres no puede expresar "ninguna fila comparte un elemento"
  con un `UNIQUE` de columna, así que `users.Repository` lo comprueba a mano en
  `Create`/`Update` (`emailTakenByAnotherUser`/`phoneTakenByAnotherUser`, ver
  `db/migrations/00002_users.sql`).
- **Comparación del correo**: sin distinguir mayúsculas/minúsculas ("foo@x.com" y
  "FOO@X.COM" cuentan como la misma dirección), igual que en `FindActiveByEmailAndPhone`.
- **Comparación del teléfono**: solo por dígitos, igual que `FindActiveByEmailAndPhone`;
  "611-222-333" y "611222333" cuentan como el mismo número.
- **Por qué**: si dos personas compartieran un correo o un teléfono, una búsqueda pensada
  para resolver a una sola persona (`FindActiveByEmailAndPhone` para el correo,
  `FindByPhoneNumber` de `internal/telegrambot` para el teléfono) acabaría resolviendo a la
  que aparezca primero, en vez de dar error.
- **Inicio de sesión**: tanto entrar como pedir el enlace de acceso funcionan contra
  cualquiera de los correos/teléfonos registrados (`Repository.FindActiveByEmailAndPhone`).
  El correo se compara sin distinguir mayúsculas, igual que `emailMatchesUser` en
  `internal/telegrambot`, así que la misma dirección funciona igual entrando desde la web
  o desde Telegram.
- **Formato**: como el espacio separa varios teléfonos, uno solo no puede llevar espacios
  internos (escribe "600111222", no "600 111 222"). Otra puntuación, como los guiones, da
  igual.

#### Directorio de personas (`GET /users/directory`)

Abierto a cualquier persona autenticada, devuelve solo login, nombre y apellidos de todas
las personas registradas (nada de teléfono, dirección, DNI...). Existe para que un socio
cualquiera pueda elegir participantes al crear una actividad sin necesitar `GET /users`,
que a un member solo le devuelve `MemberListing` de quien es o ha sido socio y a
admin/board la ficha completa.

### `magic_link_tokens`

Enlaces de acceso de un solo uso: hash del token, caducidad y marca de consumido. Junto
con el hash de un código numérico de 6 dígitos que va en el mismo correo, pensado para
cuando el correo se lee desde un dispositivo distinto al que se quiere usar para entrar:
escribir el código en `/login` es más práctico que copiar una URL larga.

#### Enlace y código

- Enlace y código representan el mismo intento de acceso en la misma fila: consumir uno
  consume también el otro (`users.Repository.ConsumeMagicLinkToken` /
  `ConsumeMagicLinkCode`).
- El token del enlace (`auth.NewMagicLinkToken`, 128 bits/16 bytes) es a propósito más
  corto que un token de sesión o de suplantación (`auth.NewOpaqueToken`, 256 bits/32
  bytes). Es el único token opaco de esta aplicación que acaba en una URL en vez de ir
  siempre en una cabecera, así que su longitud sí importa, y solo necesita resistir
  intentos de adivinarlo mientras dure `MagicLinkTTL` (15 minutos por defecto), no seguir
  siendo una credencial válida durante semanas.
- 128 bits sigue siendo el mínimo que recomienda OWASP para un identificador de sesión, y
  es astronómicamente inalcanzable por fuerza bruta en esa ventana. Eso importa porque
  `POST /auth/magic-link/consume` no tiene ningún límite de intentos propio: la entropía
  del token es la única defensa de ese endpoint.
- Un código de 6 dígitos es mucho más fácil de adivinar, así que `code_attempts` cuenta
  los intentos fallidos contra cada fila viva de esa persona y, pasado un límite
  (`maxMagicLinkCodeAttempts`, 8), deja de aceptar el código del todo, sin que eso afecte
  al enlace, que sigue siendo válido.

#### Petición y envío

- Ambos se piden en `/auth/magic-link` dando el email y el teléfono de la persona. Solo se
  generan y se envían por correo si ambos coinciden con alguien de `users` *y* esa persona
  tiene al menos uno de los roles `admin`, `board` o `member` (`auth.BaselineRoles`, ver
  «Permisos»): a quien no tiene ninguno no le serviría de nada iniciar sesión, así que
  no se le manda nada.
- La respuesta de `/auth/magic-link` es idéntica en los tres casos (no coincide nadie,
  coincide alguien sin rol, se envía el correo de verdad), así que no sirve para averiguar
  quién está en el sistema, ni quién tiene acceso, ni para mandar correo no deseado.
- `POST /auth/magic-link/consume-code` identifica a la persona por email+teléfono igual que
  la propia petición (el código por sí solo es demasiado corto para servir de clave de
  búsqueda) y responde siempre con el mismo 401 genérico, también por no distinguir casos
  desde fuera.

#### Frenos contra el abuso

- **Enfriamiento por persona** (`MagicLinkCooldown`, 60 s por defecto): si ya se le emitió
  un enlace hace menos, no se emite otro, con la misma respuesta idéntica.
- **Límite por dirección de origen** (`httpapi.IPRateLimiter`, 20 llamadas cada 15 minutos
  por defecto), compartido por `POST /auth/magic-link` y
  `POST /auth/magic-link/consume-code`. Esta última lo necesita porque su búsqueda por
  email+teléfono ocurre antes de llegar al contador `code_attempts`. `consume` no lo lleva:
  su defensa es la entropía del token.

#### Entrega por Telegram

Detalles del bot en «Telegram».

- Si la persona tiene su cuenta de Telegram vinculada, recibe el mismo enlace por Telegram
  además del correo, nunca en su lugar: quien no lo tenga vinculado, o si ese envío falla,
  sigue teniendo el correo.
- La lógica de emitir el enlace (comprobar el rol, el tiempo de espera, crear el token,
  enviarlo) vive en un único sitio interno, `authapi.Handler.issueMagicLink`, que usan
  tanto la petición web como el bot de Telegram (`internal/telegrambot.Bot`): quien ya
  tiene la cuenta vinculada, escribe al bot y demuestra su correo recibe el mismo enlace.
- **Petición web**: el DM es fire-and-forget y el correo es el respaldo; ahí
  `FindActiveByEmailAndPhone` garantiza que hay correo.
- **Flujo del bot** (`viaTelegramBot`): el DM es la única vía. `issueMagicLink` vuelve justo
  después de enviarlo y no manda correo, porque el DM ya es la respuesta a lo que la
  persona acaba de escribir. Por eso el envío es síncrono y, si falla, el error
  (`errMagicLinkTelegramDMFailed`) llega hasta `Bot.handleRecognizedChat`, que contesta con
  `magicLinkDeliveryFailedMessage` en vez de dejar a la persona sin respuesta.

#### Texto del DM

- El correo (que puede leerse desde otro dispositivo y no tiene por qué responder a nada
  que la persona acabe de hacer) siempre incluye el código de acceso y un "si no has
  pedido esto, ignora este mensaje".
- El DM copia eso tal cual cuando simplemente acompaña a una petición web.
- Cuando es el propio bot el que responde a alguien que acaba de escribirle su correo
  (`handleRecognizedChat`, ver «Telegram») omite ambas cosas: no hay ninguna página pidiendo
  ese código en ese momento, y el mensaje es precisamente la respuesta a lo que esa
  persona acaba de enviar, no algo que pueda llegar sin que lo haya pedido.

#### Borrado del DM del bot

Solo se borra el DM del flujo del bot; el de una petición web nunca, porque su código aún
puede teclearse desde otro dispositivo mucho después.

- **Temporizador**: pasado `TelegramBotDMLinkTTL` (5 minutos por defecto, más corto que el
  `MagicLinkTTL` real de 15 minutos, que sigue siendo el tiempo durante el que el enlace
  funciona; el texto del mensaje menciona esos 5). Se envía con `Client.SendMessageForID`
  en vez del `SendMessage` normal, precisamente para quedarse con el id del mensaje, y una
  goroutine, lanzada solo tras un envío correcto, espera ese tiempo y llama a
  `Client.DeleteMessage` con ese id.
- **Inicio de sesión previo**: si la persona inicia sesión antes (`Handler.
  issueSessionAndRespond`, tanto si se consumió el enlace como el código),
  `Handler.deletePendingTelegramDM` cancela la espera y borra el mensaje al instante: una
  vez usado el enlace no tiene sentido dejarlo.
- **Coordinación**: un mapa interno del handler (`pendingTelegramDMs`, con su propio mutex)
  permite que cualquiera de los dos caminos borre el mensaje sin que el otro intente
  borrarlo también después.
- **Alcance**: es solo higiene, no algo de lo que dependa la seguridad. Si el borrado
  falla, o el proceso se reinicia mientras tanto, el enlace deja de funcionar igual al
  caducar; pero uno caducado que se queda ahí es confuso, y es una cosa más expuesta si
  alguien accede más tarde al teléfono o a la sesión.

### `roles` y `user_roles`

Catálogo de roles (gestionable: crear, borrar) y quién tiene cuáles (relación muchos a
muchos).

#### Tipos de rol

- **De sistema** (`is_system`): `admin`, `board`, `member` y `former_member`. Son los únicos
  a los que el código da permisos o comportamiento automático, y no se pueden borrar.
- **Etiquetas organizativas**: cualquier otro rol que cree un admin. No desbloquea nada por
  sí solo salvo que se programe explícitamente.
- **Automáticos** (`is_automatic`): `member`, `former_member` y `board`. Nadie los asigna ni
  los quita a mano: `POST`/`DELETE /users/{login}/roles` los rechazan con 409.

#### Cálculo de los roles automáticos

Se calculan al vuelo; `roles.Repository.ListNamesForUser` es el único sitio que sabe cómo.

- `member`: a partir de `memberships`, si la persona tiene un periodo de socio activo ahora
  mismo.
- `former_member`: si ha tenido algún periodo antes pero ninguno activo hoy.
- `board`: a partir de `board_members`, si tiene un cargo de la Junta Directiva abierto
  ahora mismo.

Ese cálculo es lo que resuelve cada petición autenticada (ver «Autenticación y
sesiones»), así que los
permisos basados en "soy socio" o "soy de la junta" siempre reflejan el estado real de
alta, no una etiqueta que alguien olvidó quitar.

#### Quitar el rol `admin`

`admin` sí se asigna y quita a mano, pero `DELETE /users/{login}/roles/{roleID}` tiene dos
salvaguardas propias, aparte de la comprobación de rol automático:

- **`self_admin_removal`** (409): un admin no puede quitarse su propio rol, incluso habiendo
  otros admins de sobra. Ese click es fácil de dar por error (el botón "×" de su propia
  fila en `/member`) y, a diferencia de perder cualquier otro rol, nadie más está mirando
  en ese momento para devolvérselo: tiene que ser otro admin quien lo haga.
- **`last_admin`** (409): no se puede dejar a la última persona con admin sin ese rol, para
  que siempre quede alguien que pueda gestionar personas y roles.

`roles.Service.Remove` comprueba primero `self_admin_removal` (`actorID == userID`), ya que
es la regla más específica y no depende de cuántos admins más existan.

### `tokens`

El token de sesión de cada persona: opaco (se guarda su hash SHA-256, nunca el valor en
claro), revocable y con `expires_at` (30 días por defecto).

**Campos**

- `last_used_at`/`last_used_ip`: se actualizan en cada petición que usa el token, con la
  misma lógica de `X-Forwarded-For` que `users.last_login_ip`.

**Rutas y permisos**

- Un admin puede ver, re-caducar o revocar el token de cualquier persona:
  `GET`/`PATCH`/`DELETE /users/{login}/tokens[/{tokenID}]`. Nunca ve el valor del token,
  solo sus metadatos.

#### Impersonación

`POST /users/{login}/impersonate` (solo admin, no board: actuar como cualquiera, incluido
otro admin, es un salto mayor que el resto de lo que board ya puede hacer) crea un token
cuyo `user_id` es el de esa persona, con un `impersonated_by` (el id del admin) guardado
junto a él.

- **Efecto**: como los roles se resuelven en vivo a partir de `user_id`, igual que en
  cualquier sesión normal, esa sesión solo puede hacer lo mismo que esa persona, sin ningún
  caso especial en el resto del API.
- **Excepciones en `authapi`**: `GET /auth/me` añade `impersonated_by` a la respuesta (para
  que el frontend pueda avisarlo) y `POST /auth/impersonate/exit` deja salir de la
  impersonación pase lo que pase con los roles de la persona impersonada en ese momento.
  Por eso ese endpoint solo exige `RequireAuth`, no `Protect` (que exigiría un rol base): si
  la persona lo hubiera perdido mientras se la impersonaba, el admin se quedaría sin forma
  de salir.
- **Salir**: no reutiliza el token que tenía el admin antes de impersonar, porque el API
  nunca vuelve a tener el valor en claro de un token ya emitido. Revoca la sesión de
  impersonación y emite una nueva para el admin.
- **Restricciones**: solo se puede impersonar a quien tenga ahora mismo algún rol base
  (`auth.BaselineRoles`), igual que un enlace de acceso no se manda a quien no lo tiene, y
  nunca a uno mismo.
- **Duración**: `config.ImpersonationTokenTTL`, 1 hora frente a los 30 días de un token
  normal. Una sesión actuando como otra persona es un riesgo mayor que una normal como para
  dejarla viva un mes si alguien se olvida de salir.

### `memberships`

Periodos de alta/baja de socio. Una persona puede tener varios periodos mientras no se
solapen.

**Reglas**

- La ausencia de solapes la garantiza una restricción `EXCLUDE USING gist` de PostgreSQL
  sobre `(user_id, period)`, no una comprobación manual.
- `users.Service.enrich` pide todos los periodos de una persona
  (`memberships.Service.ListByUser`, más reciente primero) y separa las dos fechas que
  incorpora a cada `User`:
  - `membership_start_date` es la del *primer* periodo (cuándo entró por primera vez, no
    la última vez que ha vuelto a entrar tras una baja).
  - `membership_end_date` es la del periodo *más reciente* (vacía si sigue de alta).
- Así, quien ha estado varias veces en el club sigue mostrando su alta original, y la lista
  de personas puede mostrar y ordenar por fecha de alta/baja sin una petición aparte por
  persona.

**Rutas y permisos**

- `PATCH /memberships/{id}` corrige un periodo (fechas, motivo de baja) y revalida contra
  él las bajas temporales de esa misma persona que cayeran dentro del periodo *tal como
  estaba antes* de la edición: estrechar un periodo no puede verse bloqueado por una baja
  que pertenece a otro periodo suyo.
- `DELETE /memberships/{id}` (solo admin) lo borra sin más; nada lo referencia por clave
  foránea, así que nunca produce cascada. Está pensado para el «deshacer mi última
  acción» del frontend en `/member`, que solo lo ofrece para la entrada de
  alta/baja/baja-temporal más reciente de la persona; esa restricción vive en el
  frontend, no en el endpoint.

### `membership_dues`

Cuotas anuales de socio, junto a `memberships`. Cada año que un socio lo sigue siendo debe
una cuota que lo cubra: la primera, junto con la matrícula, el mismo día del alta, y luego
una más cada año siguiente.

**Campos y claves**

- Una fila por `(user_id, year)` cubierto (`UNIQUE`; `ErrDueAlreadyRecorded` si se intenta
  una segunda para el mismo año).
- Siempre ligada a la `transaction_id` que registró el pago: una cuota sin transacción
  detrás todavía no significa nada, así que, a diferencia de un periodo de alta, no hay
  fila para «impagada».
- Clave por `user_id` a propósito, no por `membership_id`: la cuota de un año de quien se
  reincorpora no está ligada de forma natural a un periodo concreto, y así un año nunca
  necesita casar con los límites de un periodo.
- `membership_dues.transaction_id` usa `ON DELETE CASCADE` (una cuota es pura anotación
  contable, sin sentido aparte de su transacción), mientras que
  `licenses.transaction_id` usa el `ON DELETE RESTRICT` por defecto: una licencia es un
  registro real con sentido propio (prueba de estar federado) incluso aparte de su pago,
  así que la transacción que la pagó no se puede borrar de debajo.

**Registro (`POST /users/{login}/dues`, solo admin)**

- Es `Service.RecordDue`. Antes de nada comprueba la transacción: tiene que existir
  (`ErrUnknownTransaction`) y tener al menos una entrada en una cuenta marcada
  `allows_dues` (`ErrTransactionNotForDues`, 409 `transaction_not_for_dues`; ver
  «Contabilidad»). Así, una cuota rechazada nunca deja un periodo de alta abierto como
  efecto secundario.
- Si la persona todavía no tiene ningún periodo de alta, se trata como su primera cuota y
  también se le abre un periodo nuevo (con la `start_date` de la petición). A quien ya
  tuvo un alta y se reincorpora se le deja enteramente al flujo de alta normal, aparte.

**Lectura (`GET /users/{login}/dues`)**

- Mismo reparto que las licencias: la propia persona o admin/board.
- Une la transacción de cada cuota para añadir `paid_at` (el `occurred_at` del pago, no
  `created_at`: la cuota de un año pasado puede registrarse mucho después de haberse
  pagado). Solo aparece en este listado, nunca en la respuesta del `POST`.

**Frontend y límites**

- En `/member`, cada cuota es un evento más («Cuota anual · año») del timeline «Historial
  como socio», con la fecha del pago y sin botones de editar ni borrar. En `/profile`, va
  en una tabla «Cuotas anuales» bajo la de altas y bajas.
- No hay endpoint para corregir o borrar una cuota, ni validación de que el año caiga
  dentro de un periodo de alta activo; ambas cosas están fuera de alcance a propósito por
  ahora.

### `membership_leaves`

«Bajas temporales»: una ventana con fecha de inicio y fecha de fin opcional (vacía para una
baja que sigue en curso, igual que el `end_date` de un periodo de socio).

**Campos y claves**

- Cuelga de la persona (`user_id`) directamente, no de un periodo de `memberships`
  concreto. Así, un único `GET /users/{login}/leaves` cubre todas las bajas que ha tenido,
  aunque hayan sido durante altas distintas, sin consultar periodo por periodo.
- Que dos bajas de la misma persona no se solapen lo garantiza
  `EXCLUDE USING gist` sobre `(user_id, period)`, igual que con los propios periodos.

**Reglas**

- La ventana tiene que quedar *contenida* en alguno de los periodos de esa persona.
  `memberships.Repository` no lo puede expresar con un `CHECK` entre dos tablas, así que
  lo valida en Go: busca qué periodo (si hay alguno) contiene por completo esas fechas
  antes de cada `INSERT`/`UPDATE` (`ErrLeaveOutsideMembership`,
  `Repository.findContainingMembership`).
- Una baja temporal no toca el periodo de socio al que pertenece, que sigue abierto
  durante ella. Por eso `users.User.OnLeave` (`on_leave` en el JSON) es el único sitio que
  distingue «activo» de «baja temporal» en la lista de personas; se calcula solo para
  quien ya tiene un alta activa, con `memberships.Service.IsOnLeave`.

**Rutas y permisos**

| Ruta | Operación | Quién |
|---|---|---|
| `/users/{login}/leaves` | listar | admin/board |
| `/users/{login}/leaves` | crear | admin |
| `/leaves/{id}` | corregir/borrar | admin |

### `licenses` y `license_types`

Licencias federativas y el catálogo de tipos. El tipo no es texto libre: se elige de un
catálogo (`license_types`, migración 00007) guardado en la base de datos en vez de un enum
de Go, porque el club espera que la lista cambie (una zona o federación nueva) y eso solo
debería necesitar un `INSERT`, no un despliegue.

**Catálogo**

- Viene sembrado con todas las combinaciones de zona (A/B/B1) × modalidad (BASICA/PLUS) ×
  franja de edad (adulto, Infantil, Juvenil), más ZERO/ASEDEB/FAE/FERM/FMM, más «Otra»
  como válvula de escape (`is_other`) para cualquier licencia que no encaje.
- Una licencia de tipo «Otra» exige además `other_detail` diciendo cuál es; esa validación
  vive en `licenses.Service.Create`, porque un `CHECK` no puede alcanzar
  `license_types.is_other` desde otra tabla.
- `GET /license-types` expone el catálogo, solo admin, igual que la propia asignación.

**Campos**

- `year` (migración 00014): el año natural que de verdad cubre, no siempre el de
  `issued_date` (una licencia pedida en diciembre suele cubrir el año siguiente).
- Es de carácter anual: válida hasta el 31 de diciembre de su `year`. `valid_until` se
  calcula en Go a partir de `year` y nunca se almacena, igual que la categoría de edad.
- `transaction_id` es opcional (nulo desde la migración 00020) y señala la transacción que
  la pagó (ver «Contabilidad»). No es editable después: corregir tipo, fecha o año es una
  acción distinta de decidir a qué pago pertenece.

**Reglas de creación (`Service.Create`)**

- Sin `transaction_id` es la única forma de registrar una licencia que el club no ha
  cobrado (por ejemplo, alguien federado en otro sitio), y entonces no hay nada más que
  comprobar sobre el pago.
- Si se da, tiene que existir (400 `unknown_transaction`) y tener al menos una entrada en
  una cuenta marcada `allows_licenses` (migración 00017; 409
  `transaction_not_for_licenses`). Esa comprobación es una consulta SQL contra las tablas
  de contabilidad, igual que la clave foránea, sin importar `internal/accounting`, y va la
  última, tras todo lo que solo depende de la petición.
- Solo se le puede asignar una licencia a quien fuera socio en algún momento del año de la
  licencia (`year`; `memberships.Service.WasMemberDuringYear`), no necesariamente ahora
  mismo ni el día de `issued_date`. Por eso se puede dar de alta una licencia de un año en
  que alguien sí era socio después de que haya causado baja, o con una fecha anterior a su
  alta de ese año. Con un año que no toca ningún periodo de socio, 409
  `not_active_member`.

**Rutas y permisos**

| Ruta | Quién |
|---|---|
| `GET /users/{login}/licenses` | la propia persona, admin o board |
| `POST /users/{login}/licenses` | admin |
| `PATCH`/`DELETE /licenses/{id}` | admin |

- `/licenses/{id}` va por id de licencia, no anidada bajo el usuario, igual que
  `/leaves/{id}`.
- `PATCH` no vuelve a exigir alta en el año, porque corrige una licencia existente en vez
  de asignar una nueva; sigue funcionando aunque la persona ya haya causado baja.

**Uso en otros sitios**

- `users.Service` incorpora a `User` solo la licencia del año actual de cada persona
  (`licenses.Service.CurrentByUser`; una de otro año no cuenta), mientras que del periodo
  de socio toma el más reciente. Es lo que muestra `GET /users`.
- En `/member`, cada licencia aparece como un evento más del timeline de altas y bajas, no
  en una sección aparte.

### `board_members`

Quién ha estado en la Junta Directiva y con qué cargo (`president`, `vice_president`,
`secretary`, `treasurer`, `member_at_large` —vocal—, tipo `board_position`), con la misma
forma que `memberships`: `start_date`/`end_date` (vacío = sigue en el cargo).

**Reglas**

- `EXCLUDE USING gist (user_id WITH =, period WITH &&)` impide que una persona tenga dos
  cargos abiertos a la vez, sean o no el mismo.
- No hay restricción sobre que dos personas tengan el mismo cargo a la vez (varios vocales
  simultáneos es normal).
- El cargo se valida en Go antes de tocar la base de datos (`Position.Valid`,
  `ErrInvalidPosition`, 400 `invalid_position`), tanto al crear como al corregir: el enum
  de PostgreSQL lo rechazaría igualmente, pero como un 500.

**Relación con otros paquetes**

- El paquete `board` no depende de `users` ni de `memberships` para calcular el rol
  automático: se calcula con una consulta SQL directa contra `board_members` desde el
  paquete `roles`, igual que `member`/`former_member` contra `memberships`. Por eso no
  necesita el juego de interfaz que sí hace falta para `membership_number` (ver
  «`users`»).
- Su `handler.go` sí define, por otro motivo, la misma interfaz mínima `UserLookup` que
  `tokens`/`roles`/`licenses`/`memberships`/`inventory`/`activities`: traducir el login de
  sus rutas `/users/{login}/...` a un id.

**Rutas y permisos**

| Ruta | Operación | Quién |
|---|---|---|
| `GET /users/{login}/board-members` | listar | la propia persona, admin o board |
| `POST /users/{login}/board-members` | crear | admin |
| `GET /board-members` | Junta actual | cualquier member (y admin/board) |
| `PATCH /board-members/{id}` | corregir cargo o fechas | admin |
| `DELETE /board-members/{id}` | borrar | admin |

- Como con un periodo de alta, nada lo referencia por clave foránea, así que borrar nunca
  produce cascada. Pero aquí no hay «mitad de cierre» a la que volver limpiando un campo:
  al ser una sola fila con inicio y fin juntos, borrar siempre quita el cargo completo, se
  pinche desde el evento que sea de los dos.
- En `/member`, un cargo aparece como dos eventos más (inicio y fin) en el timeline de
  altas y bajas, cada uno con su botón «Editar»; solo el de inicio lleva además «Borrar».

### `activities` y `activity_participants`

Actividades del club y quién participa. Crear una actividad está abierto a cualquier socio
con acceso (no solo admin/board), que además puede apuntar de una vez a los participantes
iniciales.

**Organizador y edición**

- Toda actividad tiene un organizador obligatorio (`leader_id`, expuesto como
  `leader_login`), que por defecto es quien la crea.
- Solo admin/board pueden nombrar a otra persona al crearla o reasignarla después con
  `PATCH` (403 `forbidden` para cualquier otro, también para el propio organizador, que sí
  puede editar el resto de campos).
- Editarla o cancelarla está reservado a ese organizador o a admin/board
  (`Handler.canManage`, que consulta la actividad para comparar `leader_id` con el usuario
  autenticado).
- El tipo (`caving`, `canyoning`, `training`, `social`, `other`) también se puede cambiar
  al editar.

**Validación**

- `type`, `status` y `visibility` se comprueban en Go contra los valores de sus enums
  (`Type.Valid`, `Status.Valid`, `Visibility.Valid`): uno desconocido da 400 `invalid_type`,
  `invalid_status` o `invalid_visibility`.
- `end_date` no puede ser anterior a `start_date` (400 `invalid_date_range`). Al editar se
  compara con los valores ya fusionados, así que vale enviar solo una de las dos fechas.
  La restricción `end_not_before_start` de la base de datos queda como última barrera.

**Federación**

- Apuntar a un participante en una actividad `caving` o `canyoning` exige que esa persona
  tenga una licencia federativa del año actual (`licenses.Service.IsCurrentlyFederated`);
  si no, 409 `not_federated`. Vale tanto al unirse después como al dar de alta
  participantes iniciales en la creación.

**Visibilidad y participantes**

Cada actividad tiene una `visibility` (`public`, por defecto, o `private`; migración
00005) que decide quién puede añadir participantes, no solo quién puede apuntarse.
`Handler.join` consulta `leader_id` y `visibility` antes de delegar en `Service.Join`;
`Handler.leave` (`DELETE .../participants/{login}`) hace lo simétrico.

| Acción | Actividad pública | Actividad privada |
|---|---|---|
| Apuntarse a uno mismo | cualquier member | solo organizador, board o admin lo hacen por él |
| Añadir a otra persona | admin/board | organizador, board o admin |
| Quitarse a uno mismo | cualquier member | cualquier member |
| Quitar a otra persona | admin/board | organizador, admin/board |

**Listado**

- `GET /activities?status=active` filtra a lo que todavía necesita atención (no
  planificada/cancelada, o completada pero con préstamo aún pendiente de devolver) en vez
  de todo el histórico.
- `?order=asc` cambia el orden por defecto (más reciente primero) a más antigua primero.

**`holidays`**

Festivos fijos del calendario del club, tabla propia sin relación con
`activity_participants` pero gestionada por el mismo paquete `activities`
(`activities.Handler`). `GET /holidays` está abierto a baseline; `POST`/`DELETE`, admin o
board. Solo la usa la vista de calendario del frontend.

### `activity_resources`

La memoria de una actividad: una lista ordenada de recursos que escribe quien participó en
ella después de la salida. Cada recurso es una entrada de texto (título + `description` en
texto/Markdown) o un fichero subido (título + el fichero, normalmente una foto o un PDF).

**Campos**

- `type` los distingue, con un juego de `CHECK` que mantiene cada forma consistente
  (`description` solo para texto, los campos de fichero solo para fichero).
- `position`: densa 1..N dentro de su actividad.

**Almacenamiento**

- Los metadatos viven en PostgreSQL. El contenido de un recurso de tipo fichero vive
  aparte, en disco (`config.ActivityResourcesRoot`), indexado por el id de la propia fila.
  Es un volumen Docker con nombre, no un bind mount como `library`/`userfiles`, porque
  aquí los ficheros solo llegan por `POST /activities/{id}/resources`, nunca puestos a
  mano.
- Un fichero se crea una sola vez y nunca se modifica: no hay forma de reemplazar sus
  bytes (se borra el recurso y se añade otro), y `Storage.Save` lo crea con `O_EXCL`, de
  modo que se niega a pisar uno que ya exista en vez de truncarlo. Borrar solo quita una
  entrada de directorio.
- Eso permite que otro árbol de directorios comparta los mismos ficheros con hard-links
  (ver [`environments.md`](environments.md)) sin que escribir en uno cambie el otro.

**Rutas y permisos**

- El `POST` siempre es `multipart/form-data`, incluso para una entrada de texto (sin parte
  de fichero), con un límite de 25 MiB.
- `PUT /activities/{id}/resources/order` sustituye el orden entero a la vez a partir de una
  lista completa de ids; rechaza cualquier lista que no case exactamente, en vez de
  intentar conciliar una parcial.

| Operación | Quién |
|---|---|
| Leer | cualquier rol baseline, igual que el resto de una actividad |
| Crear, reordenar | admin/board, el organizador, o cualquiera que conste como participante |
| Corregir o borrar uno concreto | solo quien lo escribió, o admin/board (aunque otro sea participante) |

### `inventory_items` e `item_movements`

Catálogo de material y sus movimientos. Los movimientos van en una única tabla con un
discriminador de tipo (`type`) en vez de una tabla por tipo, y varios `CHECK`s impiden
combinaciones sin sentido (una venta con fecha de devolución, por ejemplo).

**Ítems del catálogo** (`inventory_items.item_type`, tres formas):

- `rope` y `bag` son objetos físicos individuales: cada uno tiene su propio código y
  siempre `total_quantity` 1 (dar de alta una segunda cuerda es crear un ítem nuevo, no
  sumar unidades a uno existente).
- Campos propios de cuerda: `rope_type`, `diameter_mm`, `manufacture_year`, `length_m`.
  Campos propios de saca: `bag_type`, `capacity_liters`.
- `gear` es material genérico, archivado bajo una categoría del catálogo
  `material_categories` (`GET /material-categories`, solo admin/board). Igual que
  `license_types`, está en base de datos y no en un enum de Go, para que la lista pueda
  crecer sin desplegar código.

**Movimientos** (`item_movements.type`, ocho valores):

| Tipo | Significado |
|---|---|
| `loan` | préstamo a una actividad |
| `rental` | alquiler |
| `sale` | venta |
| `acquisition` | una nueva existencia entra al inventario |
| `loss`, `abandonment`, `retirement` | una existencia sale para siempre, sin venta detrás |
| `return` | devolución de un préstamo |

**Reglas**

- El alquiler se tarifica por semana (`rental_price_per_week`), no por día, y se cierra de
  una sola vez con `returned_at` y `status='active'→'completed'`.
  `PATCH /movements/{id}/return` solo se aplica a un alquiler abierto.
- Un préstamo o una devolución siempre van vinculados a una actividad
  (`item_movements.activity_id`, exigido por otro `CHECK`).
- Una devolución no está ligada a ningún préstamo concreto: se valida en Go contra cuánto
  de ese material tiene todavía pendiente la actividad
  (`SUM(loan.quantity) − SUM(return.quantity)` para el mismo `item_id`/`activity_id`). Así
  se pueden prestar 10 mosquetones y luego 15 más, y devolver 20 seguidos de los 5
  restantes, sin casar cada devolución con un préstamo.
- `total_quantity`, `available_quantity` y `outstanding_loan_quantity` de cada ítem nunca
  se guardan: se recalculan siempre desde su historial de movimientos
  (`inventory_item_quantities`, una vista).

**Permisos**

- Crear o modificar un movimiento requiere `admin`, `board` o el rol opcional `material`
  (sembrado por la migración pero no de sistema: un club que no quiera esa distinción puede
  borrarlo y seguir usando `board`).
- Dar de alta el propio material del catálogo está reservado a admin/board.

### Historial de una persona (`GET /users/{login}/activities` y `/movements`)

El historial de una persona visto desde su propia ficha, en vez de desde la actividad o el
movimiento.

- `GET /users/{login}/activities` está tan abierto como `listParticipants` (cualquier
  persona autenticada).
- `GET /users/{login}/movements` exige ser la propia persona o tener `admin`, `board` o
  `material`, igual que `GET /movements`, porque un movimiento puede llevar precio.

### Biblioteca de documentos (`internal/library`)

Biblioteca de documentos del club (estatutos, reglamentos, normativas, memorias de
actividades, actas de reuniones...): un árbol de directorios en disco, no PostgreSQL. Por
eso, a diferencia de cualquier otro recurso, no hay `repository.go`/pgx: `Service` trabaja
directamente contra el sistema de archivos.

**Origen de los datos**

- Raíz en `config.LibraryRoot`, montada en el contenedor desde `./library` en el host (ver
  `docker-compose.yml`).
- Los documentos los coloca quien administra el servidor, directamente en `./library`.

**Rutas y permisos**

- `GET /library?path=…` lista un directorio: subdirectorios primero, luego ficheros, en
  orden alfabético; los ficheros ocultos nunca aparecen.
- `GET /library/file/{path...}` sirve el contenido crudo de un fichero. La ruta es parte de
  la URL, no un parámetro de consulta (`/library/file/actas/2026-02-01.pdf`).
- Por defecto responde con `Content-Disposition: inline`; con `?download=1`, `attachment`.
- Acepta también la cookie de sesión (ver la sección de sesión) para que un documento sea
  un enlace normal del navegador.
- Ambas rutas son de solo lectura y están abiertas a cualquier rol baseline
  (admin/board/member). No hay forma de subir, renombrar ni borrar nada a través de la API.

**Seguridad de rutas**

Cada ruta dada por quien llama se resuelve y se comprueba contra la raíz antes de tocar el
disco (`Service.resolve`):

- Un `".."` nunca puede escapar de la raíz: se ancla primero bajo una raíz sintética `/`
  antes de limpiarla (el mismo truco que usa `net/http.FileServer`).
- Un symlink que apunte fuera del árbol se detecta resolviéndolo y comprobando que el
  destino real sigue dentro de la raíz.

### Ficheros por persona (`internal/userfiles`)

Mismo modelo que `internal/library` (árbol en disco, `config.UserFilesRoot`, montado desde
`./user-files` en el host, sin PostgreSQL), pero con un subdirectorio por login en vez de
un árbol único compartido. Guarda los documentos propios de cada persona (DNI escaneado,
una autorización firmada...).

**Rutas**

- `GET /users/{login}/files[?path=…]` y `GET /users/{login}/files/file/{path...}` calcan
  la forma de `/library` y `/library/file/{path...}`: mismo listado, `?download=1`,
  soporte de Range y la misma cookie de sesión para el fichero.

**Permisos**

- A diferencia de la biblioteca, no están abiertas a cualquier rol baseline: solo la
  propia persona o admin/board pueden verlas (403 en cualquier otro caso).
- La comprobación está dentro del propio handler, igual que `memberships.listByUser` e
  `inventory.listMovementsByUser`.

**Caso sin subdirectorio**

- Una persona sin subdirectorio propio todavía (el caso normal, hasta que alguien le deja
  algún fichero) ve un listado vacío en su propia raíz en vez de un 404.

### Contabilidad (`accounts`, `transactions`, `transaction_entries`)

El dinero del club: un plan contable en árbol (`accounts`) y, contra él, transacciones
(`transactions`) de partida doble compuestas de apuntes (`transaction_entries`).

#### Plan de cuentas (`accounts`)

**Campos**

- `name`; `code` opcional pero único si existe (un código en blanco se normaliza a
  ausente, migración 00013); `parent_id` opcional hacia otra cuenta.
- `allows_dues` y `allows_licenses` (migración 00017, ambos `false` hasta que un admin o el
  rol `money` los marca): qué cuentas tiene que usar una transacción para poder registrar
  cuotas anuales o licencias federativas, que exigen `POST /users/{login}/dues` y
  `POST /users/{login}/licenses`.
- `hidden` (migración 00019): marca una cuenta ya sin uso. No se puede borrar si tiene
  historial, así que se oculta del plan y de los selectores del frontend (que la muestran
  solo a petición) sin tocar nada de lo registrado contra ella.
- Los tres booleanos aparecen siempre en el JSON de una cuenta (también como `false`) y,
  como el resto de campos, un `PATCH` los sustituye: omitir uno lo desactiva.
- `position`: orden propio entre hermanos (mismo `parent_id`, `NULL` contado como un grupo
  más). Solo sirve para ordenar, nunca para identificar una fila.

**Reglas**

- `DELETE /accounts/{id}` solo funciona si la cuenta no tiene cuentas hijas ni apuntes
  (409 `account_in_use`).
- `PATCH` rechaza con 409 `circular_parent` un `parent_id` que sea la propia cuenta o un
  descendiente.
- `POST /accounts/{id}/move` (`{"direction": "up"|"down"}`) intercambia la posición con el
  hermano inmediatamente anterior o posterior, sin error si ya es el primero o el último de
  su grupo.

#### Transacciones y apuntes (`transactions`, `transaction_entries`)

**Estructura**

- Cada transacción se compone de dos o más apuntes (una cuenta y un importe, positivo o
  negativo) que deben sumar exactamente cero: contabilidad de partida doble estándar.
- Esa condición no puede ser un `CHECK` (solo vería su propia fila): la comprueba un
  trigger de restricción `DEFERRABLE INITIALLY DEFERRED` que se ejecuta al hacer commit,
  una vez insertados todos los apuntes de la transacción.
- Una cuenta no puede repetirse dentro de una misma transacción (restricción única
  `(transaction_id, account_id)`, migración 00018): su única entrada ya dice todo lo que
  pasó por ella.

**Errores de escritura**

| Código | Estado | Causa |
|---|---|---|
| `duplicate_account` | 400 | una cuenta repetida en la misma transacción |
| `too_few_entries` | 400 | menos de dos apuntes |
| `invalid_amount` | 400 | importe cero |
| `unknown_account` | 400 | cuenta inexistente |
| `unbalanced` | 409 | los apuntes no suman cero |

**Corrección y borrado**

- Corregir una transacción (`PATCH`) sustituye su descripción, su fecha y el conjunto
  entero de apuntes a la vez; no hay edición de un apunte suelto.
- `DELETE /transactions/{id}` la borra con todos sus apuntes y, por cascada, las cuotas que
  pagó. Si alguna licencia la señala como su pago no se puede borrar: 409
  `transaction_has_licenses` (`ErrTransactionHasLicenses`), porque `licenses.transaction_id`
  es `ON DELETE RESTRICT` — una licencia vale aunque se pierda el rastro de cómo se pagó.

#### Saldos y listados

- `GET /accounts` recorre el árbol en profundidad (cada cuenta seguida de todos sus
  descendientes antes del siguiente hermano) y añade a cada una su `balance` propio (suma
  de sus movimientos directos, nunca de los de sus hijas) y `last_transaction_at`. Ninguna
  otra ruta de `accounts` calcula estos dos campos.
- `GET /transactions` y `GET /accounts/{id}/transactions` (el libro mayor de una cuenta,
  con `balance_after` corrido) están paginados, porque crecen sin límite con los años:
  - `limit` (50 por defecto, máximo 100) y `offset`; un valor fuera de rango (`limit`
    menor o igual que 0 o mayor que 100, `offset` negativo) vuelve a su valor por defecto
    en vez de dar error.
  - Rango de fechas inclusivo `from`/`to` como `YYYY-MM-DD`: una fecha mal formada da 400
    `invalid_date` y un rango que acaba antes de empezar, 400 `invalid_date_range`.

#### Permisos

- **Lectura** (`GET` de cuentas y transacciones): abierta a cualquier rol baseline; las
  cuentas del club son algo que cualquier socio puede ver, no solo admin/board.
- **Excepción solo para admin/board**: `GET /transactions/{id}` incluye además `dues` y
  `licenses`, las cuotas y licencias que esa transacción pagó (siempre presentes para
  ellos, `[]` si no pagó ninguna; ausentes para cualquier otro rol, porque quién ha pagado
  qué es privado de esa persona, admin y board).
  - Las rellena `Handler.getTransaction` a través de dos interfaces mínimas (`DuesLookup`,
    `LicensesLookup`) que satisfacen `memberships.Service` y `licenses.Service`.
  - El diálogo de editar transacción de `/account` las muestra de solo lectura.
- **Escritura**: reservada a admin o al rol opcional `money` (sembrado por la migración,
  no de sistema, la misma forma que `material` en inventario). Board no tiene aquí ningún
  acceso de escritura especial, igual que no lo tiene sobre licencias ni sobre los datos de
  una persona.

## Backend (`backend/`)

### Capas y paquetes

El backend se separa en capas por paquete. El dominio no depende de `net/http`: el
`handler.go` de cada recurso es la única capa que sabe que existe HTTP.

- `internal/httpapi`: router y helpers HTTP.
- `internal/auth`: middleware de autenticación y autorización. No sabe nada de JWT ni de
  base de datos; solo delega en un `SessionResolver`. No hay contraseñas que gestionar.
- `internal/tokens`: el `SessionResolver` real. Valida un token opaco contra la base de
  datos, calcula los roles al vuelo y expone la gestión de tokens para admin.
- `internal/authapi`: magic-link, logout y `GET /auth/me`.
- `internal/mail`: envío de correo. Si no hay `SMTP_HOST` configurado, registra el mensaje
  en el log; si lo hay, lo manda de verdad.
- `internal/telegram` e `internal/telegrambot`: cliente de la API de bots de Telegram y bot
  que recibe mensajes (ver la subsección Telegram).
- Un paquete por recurso de dominio: `users`, `roles`, `tokens`, `memberships`,
  `licenses`, `board`, `activities`, `activityresources`, `inventory` y `accounting`.
  Cada uno se divide en `model.go`, `repository.go` (pgx), `service.go` y `handler.go`.

`internal/library` e `internal/userfiles` son la excepción a ese patrón: como no hay nada
en PostgreSQL detrás de ninguno de los dos, no tienen `repository.go`; su `service.go`
trabaja directamente contra el sistema de archivos (ver «Biblioteca de documentos» y
«Ficheros por persona»).

`GET /api/v1/health` (que es lo que alcanza un reverse proxy o systemd bajo `/api/*`) no
requiere autenticación y tiene un timeout propio de 2 s. Responde si el proceso está vivo
y, por separado, si en ese momento puede alcanzar PostgreSQL. Así se puede comprobar sin
tener ningún token de sesión que mandar.

### Autenticación y sesiones

`auth.Authenticator.Protect` es el punto único por el que se registra cada ruta. Exige
siempre tener al menos uno de los roles `admin`, `board` o `member`. Es un suelo que se
aplica además de los roles concretos que pida la ruta, no un valor por defecto que esos
roles sustituyan.

- Quien solo tenga, por ejemplo, `material` (sin ser también admin, board o socio activo)
  no puede usar el API en absoluto, ni siquiera las rutas de material.
- Como `member` es automático (se calcula de tener una alta activa ahora mismo), este suelo
  también corta el acceso en el momento en que alguien deja de ser socio.

La sesión es un único token opaco y revocable (tabla `tokens`), sin par access/refresh: la
seguridad extra de un token de acceso de vida corta no compensaba la complejidad, ni para
el cliente ni para quien opere contra el API a mano (scripts, un cliente de terminal).

- El token se usa directamente como credencial en cada petición.
- Como es opaco, el cliente no puede leer nada de él: `GET /auth/me` es la forma de saber
  quién es y qué roles tiene la sesión actual.
- Empezar una sesión requiere antes un enlace de `/auth/magic-link` (15 min de validez, un
  solo uso).

El mismo token se refleja además en una cookie `HttpOnly`/`Secure`/`SameSite=Lax`
(`katiuskas_session`, limitada a `/api/v1`) cada vez que se emite o renueva una sesión
(entrar, empezar o terminar una impersonación). `POST /auth/logout` la borra.

- Ninguna ruta normal la mira: todas siguen leyendo solo la cabecera `Authorization`.
- Existe únicamente para `auth.ProtectAllowingCookie`, que usan las rutas de fichero de
  `internal/library` e `internal/userfiles` (y solo ellas).
- Gracias a ella, un documento puede ser un enlace normal que el navegador abre en otra
  pestaña o guarda con su nombre real, sin descargarlo con `fetch` y convertirlo en un
  `Blob`.

### Primera cuenta de administración

`cmd/seed-admin` crea la primera cuenta `admin`: `POST /users` ya exige ese rol, así que
sin este comando no habría forma de entrar en una base de datos recién migrada.

- Es idempotente: no hace nada si el email ya existe.
- Como no hay contraseña, para entrar hay que pedir un enlace desde la página de acceso con
  `SEED_ADMIN_EMAIL` y `SEED_ADMIN_PHONE`, y mirar los logs de la API (o el buzón, si hay
  SMTP configurado).

### Telegram

`internal/telegram` es un cliente mínimo de la API de bots de Telegram, sin dependencias
(solo `net/http` y `encoding/json`). Se usa hoy para seis funciones independientes, cinco
de ellas "fire-and-forget" (nunca añaden latencia a lo que las dispara) y una síncrona.

#### Avisos salientes

| Aviso | Cuándo se envía |
| --- | --- |
| Logs de nivel error | Cada registro de nivel error o superior, además de registrarse normalmente. |
| Login | Un aviso por cada login (`authapi.Handler.notifyLogin`). |
| Magic-link por DM | El mismo enlace (y, según el caso, el código; ver la sección Permisos) por DM a quien tenga la cuenta vinculada (`authapi.Handler.notifyMagicLinkViaTelegram`). |
| Mutaciones | Un mensaje propio por cada acción que crea, edita o borra algo: persona, rol, membresía, licencia, actividad, material, cargo de la junta, recurso de la memoria de una actividad, token, cuenta o transacción. |
| Arranque | `cmd/api/main.go` envía "🚀 API started" justo antes de arrancar el servidor. Es la excepción síncrona (mismo límite de 10 s): una llamada única al arrancar no compite con ninguna petición, así que bloquear en ella no cuesta nada perceptible. |
| Bot | `internal/telegrambot.Bot`, el único que además *recibe* mensajes, por long polling y no por webhook (ver Bot de Telegram). |

Los avisos de mutaciones dicen quién lo hizo y, en una edición, exactamente qué cambió
campo a campo. Se construyen con `httpapi.NotifyAsync` y `DiffFields`
(`internal/httpapi/audit.go`):

- `DiffFields` compara solo los campos que ese endpoint puede tocar de verdad, nunca todos
  los del struct, para que un campo calculado aparte (como la lista de roles) no aparezca
  como un cambio que en realidad nunca ocurrió.
- Devuelve una línea "campo: antes → después" por cada campo que de verdad difiera.
- Cada paquete con rutas de escritura recibe su propio `httpapi.MutationNotifier` desde
  `cmd/api/main.go`. Los avisos son por acción significativa, no por petición, de modo que
  un simple toque de `last_used_at` en un token no avisa de nada.

Sin `TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID` configurados:

- El `Sender` es un `NoopSender` y los avisos (errores, logins, mutaciones, arranque) caen
  a registrar el mensaje en el log, igual que `mail` sin `SMTP_HOST`.
- El magic-link por Telegram simplemente no se intenta (nadie puede tener
  `telegram_chat_id` sin el bot corriendo).
- Solo el bot se omite, porque no podría avisar al canal de nada.

#### Bot de Telegram

`internal/telegrambot.Bot` recibe mensajes por long polling. El flujo depende de si el chat
ya está vinculado a una persona (`users.Repository.UserIDForTelegramChatID`).

Chat desconocido:

1. Cualquier mensaje recibe un saludo y un botón para compartir el contacto, en un teclado
   personalizado con `is_persistent: true` para que el cliente lo muestre abierto en vez de
   escondido tras su propio icono de teclado.
2. El texto del saludo explica dónde está ese botón y cómo revelarlo si el cliente no
   respeta el flag, porque nada en un teclado personalizado de Telegram es evidente para
   quien no ha visto uno antes.
3. Al recibir un contacto, `isOwnContact` comprueba que es el del propio remitente, antes
   de mirar el teléfono (ver «Contacto ajeno», más abajo en esta misma lista).
4. Si es el suyo, el teléfono se busca con `users.Repository.FindByPhoneNumber` (ver
   «Búsqueda por teléfono», más abajo).
5. Con coincidencia, se vincula su `telegram_chat_id` y `Bot.replyToLinkedContact` lo
   confirma. Sin coincidencia, se le dice y se añade `contactClubHint` ("si crees que
   deberías tener acceso, ponte en contacto con el club para comprobar tu situación", el
   mismo consejo con el que termina `unauthorizedMessage`), porque un teléfono que de verdad
   no está en la base de datos es justo el caso que merece que alguien del club lo
   compruebe a mano.
6. En ambos casos se responde con `Client.SendMessageRemovingKeyboard` en vez de un
   `SendMessage` normal (era su propio contacto, y volver a pulsar el botón solo
   reenviaría el mismo teléfono para la misma respuesta) y se avisa al canal de quién lo ha
   intentado, con qué teléfono y si coincidía.

Contacto ajeno (`isOwnContact`):

- Telegram permite adjuntar el contacto de cualquiera (una vCard escrita a mano, o el
  contacto guardado de otra persona); solo el botón "compartir contacto" que envía el
  propio bot garantiza que el contacto sea el del remitente.
- Sin esta comprobación, compartir el teléfono de un socio de verdad en nombre de otra
  persona vinculaba la cuenta de *ese socio* al chat de *quien lo enviaba*: un camino de
  suplantación, no solo un problema de calidad de datos.
- Un contacto ajeno se rechaza con una respuesta y un aviso propio al canal (más grave que
  un simple "no coincide", por ser un posible intento de suplantación), sin llegar a mirar
  el teléfono. El teclado de "compartir contacto" se queda para poder reintentarlo con el
  propio.

Búsqueda por teléfono (`FindByPhoneNumber`):

- Exige que el prefijo de país que Telegram siempre añade sea el de España ("34").
- Nunca es un simple sufijo: dejaría a un "+33666777888" francés hacerse pasar por un
  "666777888" español, ya que comparten todos los dígitos salvo el prefijo.

Confirmación de la vinculación (`Bot.replyToLinkedContact`) comprueba ahí mismo
`canUseTelegramLogin`, en vez de esperar a un mensaje posterior para descubrirlo:

- A quien puede entrar le llega `emailPrompt` como segundo mensaje aparte.
- A quien no puede le llega una única respuesta combinada
  (`unauthorizedAfterLinkReplyFormat`, "¡Gracias! Te hemos identificado como Nombre
  Apellidos. Sin embargo, este bot está disponible únicamente para usuarios autorizados.
  ..."), en vez de partir la noticia en dos mensajes sin nada útil en medio.

Chat ya vinculado (mensaje normal, no un contacto): no repite el menú de compartir
contacto, pero tampoco dispara un enlace de acceso al instante con cualquier mensaje. Eso
se probó y se revirtió por ser un retroceso de seguridad: bastaba con tener el móvil (o la
sesión de Telegram ya abierta) de un socio para entrar, sin la exigencia de conocer *a la
vez* teléfono y correo que sí tiene el flujo web. El flujo es:

1. `canUseTelegramLogin` exige el mismo rol base que cualquier ruta del API
   (`auth.HasBaselineRole`: en la práctica, un socio activo vía el rol automático
   "member", o admin/board) y además que la persona tenga correo y teléfono registrados.
   Estar vinculado no implica poder entrar: la petición de magic-link desde la web ya exige
   ambos como entrada (`FindActiveByEmailAndPhone`), pero un chat vinculado se resuelve
   directamente por su `telegram_chat_id`, así que nada garantiza que los tenga.
2. A quien no lo cumple nunca se le pide el correo: cualquier mensaje suyo recibe
   `unauthorizedMessage` ("este bot está disponible únicamente para usuarios autorizados.
   Si crees que deberías tener acceso, ponte en contacto con el club para comprobar tu
   situación").
3. A quien lo cumple, `Bot.handleRecognizedChat` le pide un segundo factor: contesta con
   `emailPrompt` ("si quieres entrar en la intranet, envíame tu dirección de correo
   electrónico") a cualquier mensaje que no sea el correo registrado de esa cuenta
   (`emailMatchesUser`, sin distinguir mayúsculas/minúsculas, contra el mismo formato de
   varios correos separados por espacios que usa `FindActiveByEmailAndPhone`).
4. `emailAttemptReply` da una respuesta concreta según *por qué* no coincidió, en vez de
   repetir siempre el aviso genérico (ver la lista siguiente). Solo una coincidencia exacta
   de una sola palabra se salta todo esto.
5. Cuando coincide, borra ese mensaje del chat con `Client.DeleteMessage` (la API de bots
   permite borrar un mensaje que el propio bot ha recibido en un chat privado), para que el
   correo no quede a la vista de quien acceda después al móvil o a la sesión. Es
   best-effort: si el borrado falla (por ejemplo, un mensaje de más de 48 h, el límite de
   Telegram), el enlace se envía igualmente.
6. Responde con un enlace de acceso nuevo, exactamente igual que si lo hubiera pedido desde
   la web (`authapi.Handler.IssueMagicLinkForTelegramUser`; ver la sección Permisos).

Respuestas de `emailAttemptReply`:

| Respuesta | Cuándo |
| --- | --- |
| `nonTextEmailReply` | Lo que llega no es texto (un `Text` vacío siempre significa eso: Telegram nunca manda un mensaje de texto de verdad con el cuerpo vacío). |
| `onlyEmailReply` | Trae más de una palabra. |
| `notAnEmailReplyFormat` | Es una sola palabra sin "@" (se repite en la respuesta). |
| `unknownEmailReplyFormat` | Tiene forma de correo pero no es ninguno de los registrados en la cuenta (se repite la dirección y se sugiere contactar con el club si cree que es un error). |

Vínculo y cambio de teléfono: el vínculo solo vale lo que valga el número con el que se
hizo. Por eso `users.Repository.Update` borra `telegram_chat_id` (lo deja en `NULL`) en
cuanto un `PATCH` cambia de verdad el teléfono de la persona, o lo deja vacío. Si no, un
chat vinculado a un número antiguo seguiría resolviendo a quien ya no lo tiene. Reenviar el
mismo teléfono, o un `PATCH` que ni siquiera lo toca, deja el vínculo tal cual.

## Frontend (`web/`)

### Mecánica compartida

Páginas multi-vista clásicas (no una SPA), sin paso de compilación: una
`<página>.html` más su `js/<página>.js` por vista, que Caddy resuelve sin extensión
(`/members` → `members.html`).

- **Cliente de API.** `web/js/api.js` es un cliente `fetch` compartido que añade el token
  (guardado en `localStorage`) a cada petición y, al cargar cada página, llama a
  `GET /auth/me` para confirmar que el token sigue siendo válido y saber qué roles tiene
  quien lo usa.
- **Estructura de página.** `js/layout.js` (`initPage()`) pinta una sola vez la barra
  lateral y la superior, valida la sesión y aplica la visibilidad por rol. Admite además
  `requireAnyRole`, que redirige al inicio a quien no tenga ninguno de los roles dados.
- **Visibilidad por rol.** Se hace con `data-roles` y es solo ocultación en el cliente:
  la autoridad es el API.
- **Formularios.** Los de crear/editar son `<dialog>` modales (`js/modal.js`,
  `initModal`); los errores que ocurren con uno abierto se muestran dentro de él.

### Páginas

| Página | Qué muestra | Quién accede | Comportamientos clave |
| --- | --- | --- | --- |
| `/` (`index.html`/`dashboard.js`) | Junta Directiva (`GET /board-members`) y enlaces a Documentos, Actas y Memorias. | Cualquier rol baseline. | Los enlaces son enlaces normales gracias a la cookie de sesión. |
| `/members` | Listado de personas. | Barra lateral: admin, board y member. | A member le reduce columnas y filtros a `MemberListing` y sus filas no son clicables; para admin y board, cada fila lleva a `/member?login=<login>`. |
| `/member?login=…` (`member.html`/`member.js`) | Ficha de una persona (ver abajo). | Solo admin y board. | Solo admin puede guardar los datos; board los ve deshabilitados. |
| `/profile` | Ficha propia: datos de contacto editables, historial, cuotas anuales, licencias y ficheros. | Cualquier rol baseline. | Incluye el mismo visor de ficheros propios que `/member`. |
| `/activities`, `/activity?id=…` | Lista y detalle de una actividad: participantes, material prestado y memoria. | Cualquier rol baseline. | La lista incluye una vista de calendario anual que marca fines de semana y festivos (no hay página de calendario aparte). |
| `/inventory` | Catálogo de material y sus movimientos. | Cualquier rol baseline. | "Nuevo material" solo para admin y board; los movimientos (préstamo, alquiler, venta), para admin, board y el rol `material`. |
| `/library` | Biblioteca de documentos del club. | Cualquier rol baseline. | `js/fileBrowser.js` comparte con `userFiles.js` la lógica de listado y vista previa; los documentos Markdown se renderizan con `js/markdown.js`. |
| `/accounting` | Plan contable en árbol, con saldo, cuentas ocultas a petición y reordenación. | Solo admin y rol `money` (sidebar y página). | Más estrecho que el API, que abre la lectura a cualquier rol baseline. |
| `/account?id=…` | Libro mayor de una cuenta. | Solo admin y rol `money`. | Alta y corrección de transacciones y, desde sus diálogos, registro de cuotas y licencias contra la transacción. |
| `login.html` | Acceso por correo y teléfono, con Telegram como segunda vía. | Sin sesión. | Es la única página sin barra lateral ni `initPage()`. |

### Ficha de una persona (`/member`)

La página reúne, para la persona consultada:

- sus datos editables (solo admin puede guardar; board los ve deshabilitados) y sus
  roles;
- el "Historial como socio": altas, bajas, bajas temporales, cuotas, licencias y cargos
  de la Junta en un solo timeline;
- su historial de actividades y de material (`GET /users/{login}/activities` y
  `GET /users/{login}/movements`);
- sus licencias federativas (`js/licenseFiles.js`: miniaturas de las copias de
  `licencias/` en los ficheros de la persona);
- sus ficheros propios (`GET /users/{login}/files`, `web/js/userFiles.js`): el mismo
  visor embebido, con las mismas migas de pan y previsualización, aparece también en
  `/profile` para que cada socio vea los suyos.

### Acceso por Telegram en `login.html`

`login.html` ofrece Telegram como segunda vía de acceso, junto al formulario de correo y
teléfono (nunca en su lugar). Al cargar, `auth.js` llama a `GET /auth/telegram-bot` (sin
autenticación, sin datos secretos: el nombre público del bot es tan público como el
enlace `t.me/<username>` que se construye con él) y, solo si viene un nombre de usuario,
muestra un aviso con enlace directo al chat del bot.

Ese nombre se configura con `TELEGRAM_BOT_USERNAME`, independiente de
`TELEGRAM_BOT_TOKEN`/`TELEGRAM_LOG_CHAT_ID` (ver «Telegram»).

## Cómo ejecutar en local

```sh
docker compose up -d postgres
export DATABASE_URL=postgres://intranet:intranet@localhost:64712/intranet?sslmode=disable
make migrate-up          # requiere goose: https://github.com/pressly/goose
make seed-admin          # crea la cuenta admin (sin contraseña: acceso por magic link)
make run                 # sirve la API en :64711
```

El frontend es estático: se puede servir con cualquier servidor de ficheros apuntando a
`web/` (o con `docker compose up` completo, que además levanta Caddy en `:64710`
proxificando `/api/*` hacia el backend).
