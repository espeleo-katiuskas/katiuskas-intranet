# Entornos: local, testing y producción

El proyecto se ejecuta en tres entornos:

| | Local | Testing | Producción |
|---|---|---|---|
| Dónde | máquina de desarrollo | host del club | host del club (la misma máquina que testing) |
| Cómo corre | Docker (`docker-compose.yml`) | unidad de systemd, sin Docker | unidad de systemd, sin Docker |
| URL | `http://localhost:64710` | `https://testing.katiuskas.es` | `https://intranet.katiuskas.es` |
| Para qué | probar los cambios antes de hacer commit o push | probar lo que ya está en `main` | las personas socias reales |
| Se actualiza con | `docker compose up --build`, a mano | cada push a `main` | cada push de una etiqueta semver |
| Base de datos | propia y desechable | propia | propia |
| Datos | los que se creen en local | copia de los de producción | los reales |
| Bot de Telegram | opcional | propio | propio |
| `LIBRARY_ROOT`, `USER_FILES_ROOT`, `ACTIVITY_RESOURCES_ROOT` | propios | compartidos con producción | compartidos con testing |

El entorno local no comparte nada con los otros dos; se levanta como se explica en la
sección "Puesta en marcha rápida" del [`README.md`](../README.md). Testing y producción se
instalan siguiendo [`deployment-no-docker.md`](deployment-no-docker.md); el resto de esta
página cubre lo específico de tener esos dos en una misma máquina.

## Cómo llegan las actualizaciones a cada entorno

- **Local:** a mano, con `docker compose up --build` (y `docker compose build api && docker
  compose up -d api` para reconstruir solo la API).
- **Un push a `main` actualiza testing automáticamente.** Por tanto, un push a `main` es un
  despliegue, no solo una forma de publicar commits: lo que llegue a `main` está corriendo
  en `testing.katiuskas.es` cuando termina la actualización.
- **Un push de una etiqueta cuyo nombre es una versión semántica (`MAJOR.MINOR.PATCH`)
  actualiza producción** al commit al que apunta esa etiqueta. Nada más cambia producción:
  los push a `main` nunca llegan hasta ella.
- Como testing sigue a `main` y producción solo se mueve con etiquetas, el flujo natural de
  una versión es etiquetar un commit que ya lleva un tiempo corriendo en testing:

  ```sh
  git tag <versión> <commit>
  git push origin <versión>
  ```

La automatización que hace estas actualizaciones no forma parte de este repositorio. El
único workflow que hay aquí, `.github/workflows/ci.yml`, revisa, compila y prueba el código
(además de validar el OpenAPI y construir la imagen de Docker) en los push a `main` y en las
pull requests; no se ejecuta con los push de etiquetas y no despliega nada. El
procedimiento manual de la guía de despliegue sigue siendo la referencia de qué implica una
actualización.

## Los datos de testing son una copia de los de producción

Cada vez que se despliega en producción, los datos de producción se copian a la base de
datos de testing. Las dos bases de datos están separadas solo porque el esquema puede ser
distinto: un commit de `main` con migraciones llega a testing antes que a producción. Esa
copia tampoco forma parte de este repositorio. Consecuencias:

- **Lo que se haga en testing se pierde en el siguiente despliegue en producción.** Altas,
  ediciones, roles, vínculos de Telegram... Entre dos despliegues testing diverge de
  producción, y la copia siguiente lo sobrescribe.
- **Testing contiene datos personales reales** de los socios (DNI, correo, teléfono,
  contacto de emergencia...), y las mismas personas tienen allí los mismos roles que en
  producción. Acceder a testing es acceder a datos reales.
- **Testing conoce los correos y teléfonos reales.** Si tiene `SMTP_*` configurado,
  enviará correos reales a socios reales; sin `SMTP_HOST`, los enlaces de acceso solo se
  registran en los logs de la API.

## Qué tiene cada entorno por su cuenta (testing y producción)

Todo lo de esta tabla tiene que ser distinto entre testing y producción:

| Ajuste | Por qué no se puede compartir |
|---|---|
| URL pública (`PUBLIC_BASE_URL`) | Los enlaces de acceso la incluyen, así que cada entorno debe apuntar a su propio dominio. |
| Base de datos (`DATABASE_URL`) | Son dos bases de datos separadas, nunca dos entornos en una, porque el esquema puede ser distinto: testing recibe cada migración primero, producción solo cuando se empuja una etiqueta. Los datos, en cambio, no son independientes (ver la sección anterior). |
| Directorio de instalación | El código, `web/`, los binarios y el fichero de entorno (`api.env`) viven en cada entorno. |
| Dirección de escucha (`HTTP_ADDR`, y el puerto del Caddy local si se usa uno) | Dos procesos no pueden escuchar en el mismo puerto, así que cada entorno necesita el suyo, y el reverse proxy de cada dominio debe reenviar al suyo. |
| Bot de Telegram (`TELEGRAM_BOT_TOKEN`, `TELEGRAM_BOT_USERNAME`) | El bot consulta Telegram con long polling, y un bot solo puede ser consultado por un proceso a la vez (una segunda consulta simultánea recibe un error de conflicto). Además, la página de acceso enlaza al bot que indica `TELEGRAM_BOT_USERNAME`, así que cada entorno debe apuntar al suyo. |

Los enlaces de cuentas de Telegram (`users.telegram_chat_id`) viven en la base de datos,
así que se copian a testing junto con el resto de los datos de producción. Pero son
identificadores de chat, y el bot de testing solo puede escribir a quien haya iniciado
antes una conversación con él.

## Qué comparten testing y producción

`LIBRARY_ROOT` (documentos del club), `USER_FILES_ROOT` (documentos de cada persona) y
`ACTIVITY_RESOURCES_ROOT` (ficheros adjuntos de las memorias de actividad) apuntan a los
mismos directorios en los dos entornos. Ambos servicios tienen que poder leerlos y
escribir donde haga falta.

- **`LIBRARY_ROOT` y `USER_FILES_ROOT`: sin conflictos de escritura.** La API solo lee
  estos árboles (nada sube, renombra ni borra a través de ella), así que un fichero que se
  deje en cualquiera de los dos directorios se ve en ambos entornos de inmediato.
- **Los ficheros de cada persona se direccionan por login.** El directorio
  `USER_FILES_ROOT/<login>/` es lo que ve la persona con ese login, y admin y junta pueden
  leer el directorio de cualquier login. Como los datos de testing son una copia de los de
  producción, los logins son los mismos en los dos entornos, y cada persona ve en testing
  los mismos documentos que en producción. La consecuencia es que **dar admin o junta a
  alguien en testing (para probar algo, por ejemplo) le da acceso de lectura a los
  documentos personales reales de todos los socios** (un DNI escaneado, una autorización
  firmada...), porque el directorio es el mismo que el de producción.
- **`ACTIVITY_RESOURCES_ROOT` sí se escribe desde la API, y eso lo hace delicado.** Cada
  fichero se guarda como `<id de actividad>/<id de recurso>`, y la API lo crea al subir un
  recurso de tipo fichero (sobrescribiendo lo que hubiera en esa ruta) y lo elimina del
  disco al borrar ese recurso. Como la base de datos de testing es una copia de la de
  producción, sus recursos tienen los mismos ids y, por tanto, las mismas rutas. Mientras el
  directorio sea el mismo:
  - **borrar en testing un recurso de tipo fichero borra el fichero real**, que producción
    sigue referenciando;
  - **subir un fichero en testing escribe en una ruta que producción puede estar usando**:
    cada base de datos asigna los ids de recurso desde su propia secuencia, y los de
    testing y producción pueden coincidir, así que un fichero puede pisar a otro.

  Hasta que este directorio deje de compartirse, subir o borrar ficheros de memorias en
  testing no es seguro; el texto de una memoria, que solo vive en la base de datos, sí se
  puede tocar sin riesgo.

## Montar el segundo entorno a partir de la guía de despliegue

La guía describe una única instalación con nombres fijos. Para el segundo entorno hay que
repetirla cambiando:

- el directorio de instalación (`/opt/katiuskas-intranet`);
- el nombre de la unidad de systemd (`katiuskas-api`) y el fichero de entorno;
- la base de datos y el rol de PostgreSQL (`intranet`);
- los puertos de escucha (`64711`, y `64710` si se usa un Caddy local);
- el dominio, tanto en el reverse proxy como en `PUBLIC_BASE_URL`;
- las variables de Telegram, con un segundo bot.

`LIBRARY_ROOT`, `USER_FILES_ROOT` y `ACTIVITY_RESOURCES_ROOT` son las únicas rutas que se
apuntan al mismo sitio en los dos; ver la sección anterior para lo que eso implica.

## Copias de seguridad

La copia de seguridad que importa es la de la base de datos de producción (ver la sección
"Copias de seguridad" de la guía de despliegue): la de testing se rellena con esos mismos
datos en cada despliegue en producción. Los tres directorios compartidos solo hace falta
copiarlos una vez.
