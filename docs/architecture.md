# Arquitectura

Documento de referencia, en sincronía con el código.

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

Tres niveles, de más a menos acceso — cada ruta lo aplica con `Authenticator.Protect`
más, cuando hace falta, una comprobación adicional dentro del propio handler (ver
`auth.BaselineRoles`, que ya exige admin/board/member en todas las rutas protegidas):

- **`admin`** — puede hacer todo, sin excepción.
- **`board`** — puede **ver** todos los datos del API (personas, socios, licencias,
  Junta Directiva, roles, actividades, material), pero solo puede **modificar**
  actividades y material — exactamente igual que antes. Ya no puede crear ni borrar
  personas, dar de alta/baja, corregir bajas temporales, asignar licencias, gestionar
  la Junta Directiva, ni asignar/quitar roles a nadie: todo eso es admin-only ahora
  (rutas bajo `/users`, `/memberships`, `/leaves`, `/users/{login}/licenses`,
  `/board-members`, `/users/{login}/roles`).
- **`member`** (socio) — puede ver `GET /users`, pero no la ficha completa de cada
  persona: solo `users.MemberListing` (nombre, apellidos, género, categoría de edad,
  número de socio, fecha de alta y de baja, `on_leave`, `roles`, y `license_type` — solo
  el tipo de la licencia federativa más reciente, nunca su fecha de obtención ni de
  validez, que siguen reservadas a admin/board) — y solo de quien es o ha sido socio
  (`membership_start_date` no nulo); a quien nunca ha tenido un alta
  (una cuenta solo-admin, por ejemplo) `Handler.list` lo excluye directamente de este
  listado para member, aunque admin/board lo sigan viendo. `age_category` (child/teen/
  adult, calculado con `ageCategoryFor` a partir de `birth_date`) sustituye a la fecha
  de nacimiento exacta, que no se expone aquí — el tramo basta para lo que hace falta
  ver de un vistazo sin revelar la fecha de nacimiento de nadie. `users.Handler.list`
  decide qué forma devolver según el rol de quien pregunta — no hay una ruta aparte.
  También puede ver `GET /board-members` (la Junta Directiva actual, con cargo,
  teléfono y correo) y ahora también `GET /roles` (el catálogo — de solo lectura para
  cualquiera, aunque asignar o quitar un rol sigue siendo cosa exclusiva de admin):
  combinando `roles` de su propia ficha con `is_automatic` del catálogo,
  `web/js/members.js` calcula la columna "Cargo" (cargo actual en la Junta, o si no
  tiene, sus roles no automáticos) igual para member que para admin/board, y con
  `on_leave` calcula "Tipo de socio" (Activo/Baja temporal/Ex-socio) también igual para
  los dos — el filtro correspondiente para member ya no ofrece "Nunca ha sido socio",
  porque `GET /users` nunca le manda a un member alguien que nunca haya sido socio.
  Sobre su propio registro, puede seguir editando datos de contacto — email, phone,
  address, postal_code, city, province, emergency_contact — pero no nombre, apellidos,
  DNI, fecha de nacimiento ni
  género: `users.Handler.update` rechaza con 403 `restricted_fields` cualquier intento
  de tocar esos campos si quien llama no es admin, en vez de ignorarlos en silencio.
  `GET /users/{login}` (la ficha completa) sigue reservado a admin/board o a la propia
  persona, sin cambios.

En el frontend, `/members` ahora es visible para member además de admin/board: la
lista de columnas y los filtros se reducen a lo que `MemberListing` realmente trae
(`MEMBER_COLUMNS`/`MEMBER_FILTERS` en `web/js/members.js`, en vez de las versiones
`FULL_*`), las filas dejan de ser clicables (`/member?login=…` sigue siendo solo para
admin/board), y las secciones de gestión (roles, nueva persona) llevan
`data-roles="admin"` o `"admin,board"` para que `applyRoleVisibility()` las quite del
todo. En `/member`, los botones de escritura (dar de alta, licencias, Junta Directiva,
gestión de roles, borrar persona) llevan `data-roles="admin"` en el HTML estático, y
las acciones que se generan dinámicamente por evento del timeline (los botones
"Editar" de cada fila) se filtran en JavaScript comprobando `isRealAdmin`, ya que
`data-roles` no llega a examinar contenido que todavía no existía cuando
`applyRoleVisibility()` se ejecutó.

## Modelo de datos (`db/migrations/`)

- **`users`** — datos personales de cualquier persona vinculada al club, incluidos
  `last_login_at` y `last_login_ip` (la IP de la última vez que inició sesión —
  `X-Forwarded-For` si existe, ya que en producción hay un proxy inverso por delante;
  la propia conexión si no). No hay tabla `accounts` ni ningún otro paso de "conceder
  acceso": lo que puede hacer una sesión depende solo de los roles de esa persona (ver
  más abajo), y esos mismos roles deciden si le llega o no un enlace de acceso — no hay
  contraseñas. `membership_number` también vive aquí, no en `memberships`: identifica a
  la persona mientras esté vinculada al club, no un periodo de alta concreto, así que
  darse de baja y volver a darse de alta nunca se lo cambia. Es un entero no negativo y
  único (`CHECK`/índice único, ver `db/migrations/00002_users.sql`),
  asignado automáticamente — nunca a mano — la primera vez que a alguien se le da de
  alta: `memberships.Service.Create` llama a `users.Service.EnsureMembershipNumber`
  (expuesta como `memberships.MembershipNumberAssigner`, ya que `memberships` no puede
  importar `users` — es al revés — así que `cmd/api`/`cmd/seed-admin` la conectan tras
  construir ambos servicios), que le pone el siguiente número tras el más alto que haya
  en ese momento, y no hace nada si la persona ya tiene uno. `gender` (tipo enum de
  PostgreSQL, `male`/`female`) es opcional y nulo por defecto — nadie lo rellena al
  crear una persona salvo que se indique explícitamente. La "categoría" (infantil,
  juvenil, mayores) no se guarda: `web/js/members.js` la calcula al vuelo a partir de
  `birth_date` y el año actual, igual que cualquier otra columna derivada de la lista.
  `email` y `phone` pueden llevar más de un valor separados por espacio — igual que
  `emergency_contact` ya admitía varios contactos en texto libre — en vez de una tabla
  aparte: encajar "alguno de estos" es una comprobación de solape de arrays
  (`string_to_array(...) && string_to_array(...)`), y esta escala de club no necesita
  más. Eso sí impide seguir usando un `UNIQUE` de columna en `email` — un mismo correo
  sigue sin poder pertenecer a dos personas, pero Postgres no puede expresar "ninguna
  fila comparte un elemento" así, de modo que `users.Repository` lo comprueba a mano en
  `Create`/`Update` (ver `db/migrations/00002_users.sql`). Iniciar sesión y
  el enlace de acceso funcionan contra cualquiera de los correos/teléfonos registrados
  (`Repository.FindActiveByEmailAndPhone`); como el espacio pasa a separar varios
  teléfonos, uno solo ya no puede llevar espacios internos como formato (escribe
  "600111222", no "600 111 222" — otra puntuación como los guiones sigue dando igual).
  `birth_date` es obligatorio (`NOT NULL`, migración `00002_users.sql`)
  — toda persona en la tabla necesita uno, no solo para calcular la "categoría". `email`,
  en cambio, es opcional — a quien no tenga ninguno registrado simplemente no le llega
  nunca un enlace de acceso, igual que a quien no tiene teléfono; los únicos campos
  realmente obligatorios al crear una persona son nombre, apellidos y fecha de
  nacimiento.
  `login` (`text UNIQUE NOT NULL`, migración `00002_users.sql`) es el único
  identificador de una persona que cruza el API — ninguna respuesta ni ruta expone jamás
  el id numérico interno. Se genera solo al crear a la persona: nombre y apellidos
  pasados por `users.slugify` (acentos fuera, todo lo que no sea letra o dígito colapsado
  a un guión) y unidos con un punto, con un sufijo numérico si esa combinación ya
  existe (`users.Repository.generateLogin`). Editable después, pero solo por admin, vía
  `PATCH /users/{login}` (formato comprobado con la misma expresión regular,
  `ErrInvalidLogin` si no cumple; `ErrDuplicate` si ya es de otra persona). Todos los
  paquetes que antes anidaban sus rutas bajo un `{userID}` numérico (`tokens`, `roles`,
  `licenses`, `memberships`, `board`, `inventory`, `activities`) reciben ahora un login y
  lo traducen al id interno con una interfaz mínima definida en cada uno de ellos
  (`UserLookup`, con `UserIDForLogin`/`LoginForUserID`) que `*users.Service` satisface de
  forma estructural — no pueden importar `users` directamente, porque `users` ya importa
  a varios de ellos (`roles`, `memberships`, `licenses`). Un listado que puede mezclar a
  varias personas a la vez (`GET /movements`, `GET /activities`...) resuelve el login
  con un `JOIN` SQL contra `users` en vez de una consulta por fila; una escritura de una
  sola fila con `INSERT`/`UPDATE ... RETURNING` (que no admite `JOIN`) lo resuelve a
  partir de un login que el handler ya tiene a mano, o con una llamada de vuelta a
  `LoginForUserID`.
- **`magic_link_tokens`** — enlaces de acceso de un solo uso (hash del token, caducidad,
  marca de consumido), junto con el hash de un código numérico de 6 dígitos que va en el
  mismo correo — pensado para cuando el correo se lee desde un
  dispositivo distinto al que se quiere usar para entrar: escribir el código en
  `/login` es más práctico ahí que copiar una URL larga. Enlace y código representan el
  mismo intento de acceso en la misma fila; consumir uno consume también el otro
  (`users.Repository.ConsumeMagicLinkToken` / `ConsumeMagicLinkCode`). Como un código de
  6 dígitos es mucho más fácil de adivinar por fuerza bruta que el token del enlace,
  `code_attempts` cuenta los intentos fallidos contra cada fila viva de esa persona y,
  pasado un límite (`maxMagicLinkCodeAttempts`), deja de aceptar el código del todo —
  sin que eso afecte al enlace, que sigue siendo válido igual. Ambos se piden en
  `/auth/magic-link` dando el email y el teléfono de la persona; solo se generan y se
  envían por correo si ambos coinciden con alguien de `users` *y* esa persona tiene al
  menos uno de los roles `admin`, `board` o `member` (`auth.BaselineRoles`, ver más
  abajo) — a quien no tiene ninguno no le serviría de nada iniciar sesión, así que no se
  le manda nada. La respuesta de `/auth/magic-link` es idéntica en los tres casos (no
  coincide nadie, coincide alguien sin rol, se envía el correo de verdad) — así no sirve
  para averiguar quién está en el sistema, ni quién tiene acceso, ni para mandar correo
  no deseado. `POST /auth/magic-link/consume-code` identifica a la persona por
  email+teléfono igual que la propia petición (el código por sí solo es demasiado corto
  para servir de clave de búsqueda) y responde siempre con el mismo 401 genérico,
  también por no distinguir casos desde fuera.
- **`roles`** / **`user_roles`** — catálogo de roles (gestionable: crear, borrar) y
  quién tiene cuáles (muchos a muchos). `admin`, `board`, `member` y `former_member`
  son roles de *sistema* (`is_system`): son los únicos a los que el código realmente
  da permisos o comportamiento automático, y no se pueden borrar. Cualquier otro rol
  que cree un admin sirve como etiqueta organizativa — no desbloquea nada por sí solo
  salvo que se programe explícitamente.
  `member`, `former_member` y `board` son además *automáticos* (`is_automatic`): nadie
  los asigna ni los quita a mano (`POST`/`DELETE /users/{login}/roles` los rechaza con
  409). Se calculan al vuelo — `roles.Repository.ListNamesForUser` es el único sitio que
  sabe cómo—: `member` a partir de `memberships` si la persona tiene un periodo de socio
  activo ahora mismo, `former_member` si ha tenido alguno antes pero ninguno activo hoy,
  y `board` a partir de `board_members` si tiene un cargo de la Junta Directiva abierto
  ahora mismo. Ese cálculo es lo que resuelve cada petición autenticada (ver más abajo),
  así que los permisos basados en "soy socio" o "soy de la junta" siempre reflejan el
  estado real de alta, no una etiqueta que alguien olvidó quitar.
- **`tokens`** — el token de sesión de cada persona: opaco (hash SHA-256 guardado, nunca
  el valor en claro), revocable, con `expires_at`, y `last_used_at`/`last_used_ip` (misma
  lógica de `X-Forwarded-For` que `users.last_login_ip`) actualizados en cada petición
  que lo usa. Un admin puede ver, re-caducar o revocar el token de cualquier persona —
  `GET`/`PATCH`/`DELETE /users/{login}/tokens[/{tokenID}]` —, pero nunca ver el valor
  del token en sí, solo sus metadatos.
  **Impersonación**: `POST /users/{login}/impersonate` (solo admin, no board — actuar
  como cualquiera, incluido otro admin, es un salto mayor que el resto de lo que board ya
  puede hacer) crea un token cuyo `user_id` es el de esa persona y con un
  `impersonated_by` (el id del admin) guardado junto a él. Como los roles se resuelven en
  vivo a partir de `user_id` (igual que cualquier sesión normal), esa sesión ya sólo puede
  hacer lo mismo que esa persona sin ningún caso especial en el resto del API — la única
  excepción vive en `authapi`: `GET /auth/me` añade `impersonated_by` a la respuesta
  (para que el frontend pueda avisarlo) y `POST /auth/impersonate/exit` deja salir de la
  impersonación pase lo que pase con los roles de la persona impersonada en ese momento
  (por eso ese endpoint sólo exige `RequireAuth`, no `Protect`, que exigiría un rol base —
  si la persona lo hubiera perdido mientras se la impersonaba, ese admin se quedaría sin
  forma de salir). Salir no reutiliza el token que tenía el admin antes de impersonar —el
  API nunca vuelve a tener el valor en claro de un token ya emitido—, sino que revoca la
  sesión de impersonación y emite una nueva para el admin. Sólo se puede impersonar a
  quien tenga ahora mismo algún rol base (`auth.BaselineRoles`) — igual que un enlace de
  acceso no se manda a quien no lo tiene —, y nunca a uno mismo. El token de impersonación
  dura mucho menos que uno normal (`config.ImpersonationTokenTTL`, 1 hora frente a 30
  días): una sesión actuando como otra persona es un riesgo mayor que una normal como para
  dejarla viva un mes si alguien se olvida de salir.
- `GET /users/directory` — abierto a cualquier persona autenticada, devuelve solo login y
  nombre de quienes tienen acceso (nada de teléfono, dirección, DNI...). Existe para que
  un socio cualquiera pueda elegir participantes al crear una actividad sin que el API
  tenga que abrirle `GET /users`, que sí expone datos personales y sigue reservado a
  admin/board.
- **`memberships`** — periodos de alta/baja de socio. Una persona puede tener varios
  periodos si no se solapan; lo garantiza una restricción `EXCLUDE USING gist` de
  PostgreSQL sobre `(user_id, period)`, no una comprobación manual.
  `users.Service.enrich` pide todos los periodos de una persona
  (`memberships.Service.ListByUser`, más reciente primero) y separa las dos fechas que
  incorpora a cada `User`: `membership_start_date` es la del *primer* periodo (cuándo
  entró por primera vez, no la última vez que ha vuelto a entrar tras una baja) y
  `membership_end_date` la del periodo *más reciente* (o vacía si sigue de alta) — para
  que alguien que ha estado varias veces en el club siga mostrando su alta original en
  vez de la fecha de su último reingreso. Así la lista de personas puede mostrar y
  ordenar por fecha de alta/baja sin una petición aparte por persona.
- **`membership_leaves`** — "bajas temporales": una ventana con fecha de inicio y fin
  (ambas obligatorias desde el principio, a diferencia de un periodo de socio) dentro
  de un periodo de `memberships` concreto — la persona se ausenta un tiempo sin dejar
  de ser socia. Que la ventana quede *contenida* en el periodo de socio no es algo que
  una restricción `CHECK` pueda comprobar entre dos tablas, y este proyecto no usa
  triggers, así que `memberships.Repository` lo valida en Go antes de cada
  `INSERT`/`UPDATE` (`ErrLeaveOutsideMembership`); que dos bajas del mismo periodo no
  se solapen sí lo garantiza `EXCLUDE USING gist`, igual que con los propios periodos.
  Rutas bajo `/memberships/{membershipID}/leaves` y `/leaves/{id}`, admin/board. Una
  baja temporal no toca el periodo de socio al que pertenece — sigue abierto durante
  ella —, así que `users.User.OnLeave` (`on_leave` en el JSON) es el único sitio que
  distingue "activo" de "baja temporal" en la lista de personas; se calcula solo para
  quien ya tiene un alta activa, con `memberships.Service.IsOnLeave`.
- **`licenses`** / **`license_types`** — licencias federativas. El tipo ya no es texto
  libre: se elige de un catálogo (`license_types`, migración 00007) guardado en la base
  de datos en vez de un enum de Go, precisamente porque el club espera que la lista
  cambie con el tiempo (una zona nueva, una federación nueva) y eso no debería necesitar
  un despliegue de código — solo un `INSERT`. El catálogo trae sembradas todas las
  combinaciones de zona (A/B/B1) × modalidad (BASICA/PLUS) × franja de edad (adulto,
  Infantil, Juvenil), más ZERO/ASEDEB/FAE/FERM/FMM, más "Otra" como válvula de escape
  (`is_other`) para cualquier licencia que no encaje: una licencia de tipo "Otra" exige
  además `other_detail` diciendo cuál es en realidad, validación que vive en
  `licenses.Service.Create` (un `CHECK` no puede alcanzar `license_types.is_other` desde
  otra tabla). `GET /license-types` expone el catálogo, solo admin, igual que la propia
  asignación. De carácter anual: válida desde `issued_date` hasta el 31 de diciembre de
  ese mismo año (`valid_until`, calculado en Go a partir de `issued_date`, nunca
  almacenado — igual que la "categoría" de `web/js/members.js`, no vale la pena guardar
  algo que se deriva trivialmente de otro campo). Solo se le puede asignar una licencia a
  quien tuviera un periodo de `memberships` abierto en la fecha de `issued_date` de esa
  licencia — no necesariamente ahora mismo (`memberships.Service.WasMemberOn`), así que
  una licencia de cuando alguien sí era socio se puede dar de alta después de que haya
  causado baja. Intentarlo con una fecha que nunca cayó dentro de ningún periodo de socio
  devuelve 409 `not_active_member`. `users.Service` incorpora la
  licencia más reciente de cada persona a `User` (`licenses.Service.LatestByUser`), igual
  que hace con el periodo de socio más reciente. Rutas bajo `/users/{login}/licenses`:
  `GET` abierto a la propia persona o admin/board, `POST` solo admin;
  `PATCH`/`DELETE /licenses/{id}` (por id de licencia, no anidadas bajo el usuario, igual
  que `/leaves/{id}`) también solo admin — `PATCH` no vuelve a exigir alta activa, porque
  corrige una licencia ya existente en vez de asignar una nueva, así que sigue
  funcionando aunque la persona ya haya causado baja. En `/member`, cada
  licencia aparece como un evento más dentro del mismo timeline de altas y bajas, no en
  una sección aparte.
- **`board_members`** — quién ha estado en la Junta Directiva y con qué cargo
  (`president`/`vice_president`/`secretary`/`treasurer`/`member_at_large` — vocal, tipo
  `board_position`), con la misma forma que `memberships`: `start_date`/`end_date`
  (vacío = sigue en el cargo) y un `EXCLUDE USING gist (user_id WITH =, period WITH &&)`
  que impide que una persona tenga dos cargos abiertos a la vez, sean o no el mismo — no
  hay restricción, en cambio, sobre que dos personas tengan el mismo cargo simultáneamente
  (por ejemplo varios vocales a la vez es normal). El paquete `board` no depende de
  `users` ni de `memberships` para calcular el rol automático — no necesita el juego de
  interfaz que sí hace falta para `membership_number` (ver más abajo), porque `board` se
  calcula con una consulta SQL directa contra `board_members` desde el propio paquete
  `roles`, igual que `member`/`former_member` contra `memberships`. Su `handler.go` sí
  define, aparte y por un motivo distinto, la misma interfaz mínima `UserLookup` que
  `tokens`/`roles`/`licenses`/`memberships`/`inventory`/`activities` — para traducir el
  login de sus rutas `/users/{login}/...` a un id, no para nada relacionado con el rol
  automático. Rutas bajo
  `/users/{login}/board-members` (`GET` propia persona o admin/board, `POST` solo
  admin), `GET /board-members` (la Junta Directiva actual, abierta también a member), y
  `PATCH /board-members/{id}` para corregir cargo o fechas. En `/member`, un cargo
  aparece como dos eventos más (inicio y fin) en el mismo timeline de altas y bajas.
- **`activities`** / **`activity_participants`** — actividades del club y quién participa.
  Crear una actividad está abierto a cualquier socio con acceso (no solo admin/board), que
  además puede apuntar de una vez a los participantes iniciales. Toda actividad tiene un
  organizador obligatorio (`leader_id`), fijado siempre a quien la crea — nunca elegible
  por quien llama al endpoint —, y editarla o cancelarla está reservado a ese organizador
  o a admin/board (`Handler.canManage`, que consulta la actividad para comparar
  `leader_id` con el usuario autenticado). El tipo (`caving`, `canyoning`, `training`,
  `social`, `other`) también se puede cambiar al editar. Apuntar a un participante en una
  actividad de tipo `caving` o `canyoning` exige que esa persona tenga una licencia
  federativa vigente (`licenses.Service.IsCurrentlyFederated`); si no, la llamada se
  rechaza con 409 `not_federated` — tanto al unirse después como al dar de alta
  participantes iniciales en la propia creación. Cada actividad tiene también una
  `visibility` (`public`, valor por defecto, o `private`, migración 00005) que decide
  quién puede añadir participantes, no solo quién puede apuntarse: en una pública
  cualquiera puede apuntarse a sí mismo y admin/board puede añadir a quien sea, igual que
  siempre; en una privada solo el organizador, board o admin pueden dar de alta a un
  participante — ni siquiera un socio cualquiera puede apuntarse a sí mismo sin que uno de
  esos tres lo haga por él (`Handler.join`, que consulta `leader_id` y `visibility` antes
  de delegar en `Service.Join`). `GET /activities?status=active` filtra a lo que todavía
  necesita atención (no planificada/cancelada, o completada pero con préstamo aún
  pendiente de devolver) en vez de todo el histórico; `?order=asc` cambia el orden por
  defecto (más reciente primero) a más antigua primero. **`holidays`** — festivos fijos
  del calendario del club, tabla propia sin relación con `activity_participants` pero
  gestionada por el mismo paquete `activities`/`activities.Handler` (`GET /holidays`
  abierto a baseline, `POST`/`DELETE` admin/board) — usada solo por la vista de calendario
  del frontend.
- **`inventory_items`** / **`item_movements`** — catálogo de material y sus movimientos,
  modelados en una única tabla con un discriminador de tipo (`type`) en vez de una tabla
  por tipo, con `CHECK`s que impiden combinaciones sin sentido (una venta con fecha de
  devolución, por ejemplo). Un `inventory_items.item_type` distingue tres formas: `rope`
  y `bag` son objetos físicos individuales, cada uno con su propio código y siempre
  `total_quantity` 1 (dar de alta una segunda cuerda es dar de alta un ítem nuevo, no
  sumar unidades a uno existente), con campos propios de su tipo (`rope_type`/
  `diameter_mm`/`manufacture_year`/`length_m` para cuerda; `bag_type`/`capacity_liters`
  para saca); `gear` es material genérico, archivado bajo una categoría del catálogo
  `material_categories` (`GET /material-categories`, admin/board — igual que
  `license_types`, guardado en base de datos en vez de un enum de Go, para que la lista
  pueda crecer sin desplegar código). `item_movements.type` tiene ocho valores: `loan`,
  `rental`, `sale`, `acquisition` (nueva existencia entra al inventario),
  `loss`/`abandonment`/`retirement` (existencia sale para siempre, sin venta detrás) y
  `return`, la devolución de un préstamo. El alquiler se tarifica por semana
  (`rental_price_per_week`), no por día, y sigue cerrando con
  `returned_at`/`status='active'→'completed'` de una sola vez —
  `PATCH /movements/{id}/return` solo se aplica a un alquiler abierto. Un préstamo o una
  devolución siempre van vinculados a una actividad (`item_movements.activity_id`,
  exigido por otro `CHECK`); una devolución no está ligada a ningún préstamo concreto —
  se valida en Go contra cuánto de ese material tiene esa actividad todavía pendiente
  (`SUM(loan.quantity) − SUM(return.quantity)` para el mismo `item_id`/`activity_id`),
  lo que permite, por ejemplo, prestar 10 mosquetones y luego 15 más, y devolver 20
  seguido de los 5 restantes, sin que cada devolución tenga que casar con un préstamo
  concreto. `total_quantity`/`available_quantity`/`outstanding_loan_quantity` de cada
  ítem nunca se guardan — se recalculan siempre desde su propio historial de movimientos
  (`inventory_item_quantities`, una vista). Crear o modificar un movimiento requiere el
  rol `admin`, `board` o el rol opcional `material` (sembrado por la migración pero no de
  sistema: un club que no quiera esa distinción puede borrarlo y seguir usando `board`);
  dar de alta el propio material del catálogo sigue reservado a admin/board.
- `GET /users/{login}/activities` y `GET /users/{login}/movements` — el historial de una
  persona visto desde su propia ficha, en vez de desde la actividad o el movimiento. El
  primero está tan abierto como `listParticipants` (cualquier persona autenticada); el
  segundo exige ser la propia persona o tener `admin`, `board` o `material`, igual que
  `GET /movements`, porque un movimiento puede llevar precio.
- **`internal/library`** — biblioteca de documentos del club (estatutos, reglamentos,
  normativas, memorias de actividades, actas de reuniones...): un árbol de directorios en
  disco (`config.LibraryRoot`, montado de solo lectura en el contenedor desde `./library`
  en el host — ver `docker-compose.yml`), no nada en PostgreSQL, así que a diferencia de
  cualquier otro recurso no hay `repository.go`/pgx: `Service` trabaja directamente contra
  el sistema de archivos. `GET /library?path=…` lista un directorio (subdirectorios
  primero, luego ficheros, alfabético; los ficheros ocultos nunca aparecen);
  `GET /library/file?path=…` sirve el contenido crudo de un fichero, con
  `Content-Disposition: inline` por defecto o `attachment` con `?download=1`. Ambas rutas
  son de solo lectura y abiertas a cualquier rol baseline (admin/board/member) — no hay
  forma de subir, renombrar ni borrar nada a través de la API; los documentos los coloca
  quien administra el servidor, directamente en `./library`. Cada ruta dada por quien
  llama se resuelve y se comprueba contra la raíz antes de tocar el disco
  (`Service.resolve`): un `".."` nunca puede escapar de la raíz porque se ancla primero
  bajo una raíz sintética `/` antes de limpiarla (el mismo truco que usa el propio
  `net/http.FileServer`), y un symlink que apunte fuera del árbol se detecta resolviéndolo
  y comprobando que el destino real sigue dentro de la raíz.
- **`internal/userfiles`** — el mismo modelo que `internal/library` (árbol en disco,
  `config.UserFilesRoot`, montado desde `./user-files` en el host, sin PostgreSQL de por
  medio), pero con un subdirectorio por login en vez de un árbol único compartido: los
  documentos propios de cada persona (DNI escaneado, una autorización firmada...).
  `GET /users/{login}/files[?path=…]` y `GET /users/{login}/files/file?path=…` calcan la
  forma de `/library`/`/library/file` (mismo listado, `?download=1`, soporte de Range),
  pero, a diferencia de la biblioteca, no están abiertas a cualquier rol baseline: solo
  la propia persona o admin/board pueden verlas (403 en cualquier otro caso), comprobado
  dentro del propio handler igual que `memberships.listByUser`/
  `inventory.listMovementsByUser`. Una persona sin subdirectorio propio todavía — el caso
  normal, hasta que alguien le deja algún fichero — ve un listado vacío en su propia raíz
  en vez de un 404.

## Backend (`backend/`)

Capas separadas por paquete: `internal/httpapi` (router y helpers HTTP), `internal/auth`
(middleware de autenticación/autorización — ya no hay contraseñas que gestionar; no sabe
nada de JWT ni de base de datos, solo delega en un `SessionResolver`), `internal/tokens`
(el `SessionResolver` real: valida un token opaco contra la base de datos, calcula los
roles al vuelo, y expone la gestión de tokens para admin), `internal/mail` (envío de
correo: registra el mensaje en el log si no hay `SMTP_HOST` configurado, lo manda de
verdad si lo hay), `internal/telegram` (cliente mínimo de la API de bots de Telegram, sin
dependencias — solo `net/http`/`encoding/json` —, usado hoy para cinco avisos
independientes. Cuatro son "fire-and-forget" y nunca añaden latencia a lo que los
dispara: los
logs de nivel error, un aviso de cada login (`authapi.Handler.notifyLogin`), un aviso de
cada llamada al API que sea POST/PUT/PATCH/DELETE (`httpapi.NotifyMutatingCalls`, en el
propio router — no un wrapper de la base de datos: la primera versión de esto avisaba de
cada cambio en la base de datos en vez de cada llamada, y como tocar la fecha del último
uso de un token es en sí un UPDATE, avisaba en cada petición autenticada, no solo en las
que de verdad cambiaban algo), y —el único
que además *recibe* mensajes, por long polling, no webhook— `internal/telegrambot.Bot`:
si alguien desconocido escribe al bot, le pide que comparta su contacto, y si el teléfono
compartido coincide con el de un socio (`users.Repository.FindByPhoneNumber`, exigiendo
que el prefijo de país que Telegram siempre añade sea el de España — nunca un simple
sufijo, que dejaría a un "+33666777888" francés hacerse pasar por un "666777888" español,
ya que comparten todos los dígitos salvo el prefijo) vincula su `telegram_chat_id` y
se lo confirma; si no coincide, se lo dice; en ambos casos avisa al canal de quién lo ha
intentado, con qué teléfono, y si coincidía o no. El quinto es la excepción a
"fire-and-forget": `cmd/api/main.go` envía "🚀 API started" justo antes de arrancar el
servidor, de forma síncrona (con el mismo límite de 10s) — una llamada única al arrancar
el proceso no compite con ninguna petición, así que bloquear en ella no cuesta nada
perceptible (ver `CLAUDE.md` para el detalle de los cinco). Los dos primeros (logs de
error, login) y el de arranque son incondicionales — sin `TELEGRAM_BOT_TOKEN`/
`TELEGRAM_LOG_CHAT_ID` configurados, caen a registrar el mensaje en el log (igual que
`mail` sin `SMTP_HOST`) — pero ni el aviso de mutaciones ni el bot se activan siquiera
sin esas variables, para no generar una línea de log por cada escritura ni un bot que no
podría avisar al canal de nada), `internal/authapi` (magic-link/logout/me) y un paquete por recurso de
dominio (`users`, `roles`, `memberships`, `licenses`, `board`, `activities`, `inventory`), cada uno con
`model.go` / `repository.go` (pgx) / `service.go` / `handler.go`. El dominio no depende
de `net/http`; el handler es la única capa que sabe que existe HTTP. `internal/library` e
`internal/userfiles` son la excepción a ese patrón de capas: como no hay nada en
PostgreSQL detrás de ninguno de los dos, ninguno tiene `repository.go` —
`service.go` trabaja directamente contra el sistema de archivos en ambos (ver más
arriba).

`auth.Authenticator.Protect`, el punto único por el que cada ruta se registra, exige
siempre tener al menos uno de los roles `admin`, `board` o `member` — un suelo que se
aplica *además* de los roles concretos que pida esa ruta, no un valor por defecto que
esos roles sustituyan. Alguien con solo, por ejemplo, `material` (sin ser también
admin, board o socio activo) no puede usar el API en absoluto, ni siquiera las rutas
de material. Como `member` es automático (se calcula de tener una alta activa ahora
mismo), esto también corta el acceso en el momento en que alguien deja de ser socio.

Sesión: un único token opaco y revocable (tabla `tokens`), sin par access/refresh — la
seguridad extra de un token de acceso de vida corta no compensaba la complejidad, ni
para el cliente ni para quien tuviera que operar contra el API a mano (scripts, un
cliente de terminal). El token se usa directamente como credencial en cada petición;
como es opaco, el cliente no puede leer nada de él — `GET /auth/me` es la forma de
saber quién es y qué roles tiene la sesión actual. Empezarla requiere primero un enlace
de `/auth/magic-link` (15 min de validez, un solo uso).

`cmd/seed-admin` crea la primera cuenta `admin`: `POST /users` ya exige ese rol, así que
sin este comando no habría forma de entrar en una base de datos recién migrada.
Idempotente — no hace nada si el email ya existe. Como no hay contraseña, para entrar
hay que pedir un enlace desde la página de acceso con `SEED_ADMIN_EMAIL` y
`SEED_ADMIN_PHONE`, y mirar los logs de la API (o el buzón, si hay SMTP configurado).

## Frontend (`web/`)

Páginas multi-vista clásicas (no una SPA), con un cliente `fetch` compartido
(`web/js/api.js`) que añade el token (guardado en `localStorage`) a cada petición y, al
cargar cada página, llama a `GET /auth/me` para confirmar que sigue siendo válido y
saber qué roles tiene quien lo usa.

Cada fila de la tabla de personas en `/members` enlaza a `/member?login=<login>`
(`member.html`/`member.js`, solo admin/board): la ficha de esa persona, con sus datos
editables, su historial de actividades y material (`GET /users/{login}/activities` y
`GET /users/{login}/movements`) y sus ficheros propios (`GET /users/{login}/files`,
`web/js/userFiles.js` — el mismo visor embebido, con las mismas migas de pan y
previsualización, aparece también en `/profile` para que cada socio vea los suyos).
`/library` es la biblioteca de documentos del club, abierta a cualquier rol baseline.

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
