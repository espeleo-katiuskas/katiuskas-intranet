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
distinto: un commit de `main` con migraciones llega a testing antes que a producción. Quien
dispara esa copia en cada despliegue no forma parte de este repositorio, pero sí el script
que la hace, [`scripts/copy-prod-to-testing.sh`](../scripts/copy-prod-to-testing.sh):
vacía la base de datos de testing, restaura en ella un volcado completo de la de producción
(esquema, datos, valores de las secuencias y la tabla de versiones de goose) y aplica con
`goose` las migraciones en las que testing va por delante. Además, salvo con `--no-files`,
sustituye los ficheros de `ACTIVITY_RESOURCES_ROOT` de testing por hard-links a los de
producción (ver la sección "Opciones para `ACTIVITY_RESOURCES_ROOT`"). Producción solo se
lee. Se ejecuta como root o como un usuario que pueda parar y arrancar la unidad de testing:

```sh
TESTING_SERVICE=<unidad de testing> \
  scripts/copy-prod-to-testing.sh [--yes] [--no-files] <api.env de producción> <api.env de testing>
```

Si ese usuario lo hace con `sudo`, `SYSTEMCTL="sudo systemctl"` (admite argumentos, por
ejemplo `"sudo -n systemctl"`) hace que pare y arranque la unidad con él. Es lo único que
usa `sudo`: el resto no necesita privilegios, pero sí poder leer los dos `api.env` (la guía
de despliegue los deja con `chmod 600`, propiedad de `katiuskas`) y, para el paso de
ficheros, ser el dueño de los de producción (`katiuskas`) o root.

Lee `DATABASE_URL` y `ACTIVITY_RESOURCES_ROOT` de los dos `api.env` (sin ejecutarlos) y
necesita `psql`, `pg_dump` y el binario de `goose`; pide teclear el nombre de la base de
datos de testing antes de borrarla (`--yes` lo omite, para automatizarlo); se niega a
continuar si las dos URLs apuntan a la misma base de datos, si el rol de testing no es el
dueño del esquema `public` o si producción tiene una migración que el código de testing no
conoce; y, si algo falla una vez parada la unidad, la deja parada. Consecuencias de la
copia:

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
  fichero se guarda como `<id de actividad>/<id de recurso>`; la API lo crea al subir un
  recurso de tipo fichero (y se niega, con un error, a pisar uno que ya exista) y lo
  elimina del disco al borrar ese recurso. Como la base de datos de testing es una copia de
  la de producción, sus recursos tienen los mismos ids y, por tanto, las mismas rutas.
  Mientras el directorio sea el mismo:
  - **borrar en testing un recurso de tipo fichero borra el fichero real**, que producción
    sigue referenciando;
  - **subir un fichero en testing ocupa una ruta que producción puede necesitar después**:
    cada base de datos asigna los ids de recurso desde su propia secuencia, y los de
    testing y producción pueden coincidir. Quien suba segundo recibe un error (500), sea
    testing o producción; sin esa negativa a pisar, el segundo habría sobrescrito en
    silencio el fichero del primero.

  Hasta que este directorio deje de compartirse, subir o borrar ficheros de memorias en
  testing no es seguro; el texto de una memoria, que solo vive en la base de datos, sí se
  puede tocar sin riesgo. Las opciones para dejar de compartirlo están en la sección
  siguiente.

## Opciones para `ACTIVITY_RESOURCES_ROOT`

1. **Compartido (la situación actual).** No hay copia ni espacio extra, pero sí los dos
   riesgos de arriba: solo es seguro si en testing no se suben ni se borran ficheros de
   memorias.
2. **Directorio propio de testing, con una copia normal de producción.** Cada despliegue
   en producción vuelve a copiar el directorio, siempre de producción hacia testing (por
   ejemplo con `rsync -a --delete`). Aísla por completo los dos entornos; el coste es el
   espacio y el tiempo de copia, porque son fotos y PDFs.
3. **Directorio propio de testing, copiado con hard-links.** Los ficheros no se duplican:
   cada uno es el mismo inodo con una entrada en cada árbol, así que testing no ocupa
   espacio extra (salvo los directorios y lo que se suba en testing). Después de la copia,
   subir un fichero en testing crea uno nuevo solo en testing, y borrar un recurso en
   testing deja intacto el fichero de producción. Es seguro porque la API nunca modifica un
   fichero ya guardado:
   - al crear, `Storage.Save` abre el fichero con `O_EXCL`, así que nunca trunca uno
     existente, que es lo único que cambiaría el contenido en los dos árboles a la vez;
   - al borrar, `os.Remove` quita solo la entrada de ese árbol, y el fichero sigue en el
     otro;
   - no hay ninguna operación que reescriba los bytes de un recurso (para cambiarlos hay
     que borrar el recurso y crear otro).

   Condiciones:
   - los dos directorios tienen que estar en el mismo sistema de ficheros (`stat -c %d`
     da el mismo número para ambos);
   - la copia tiene que hacerla el usuario dueño de los ficheros (`katiuskas`) o root: con
     `fs.protected_hardlinks=1`, que es lo habitual, Linux no deja crear un hard-link a un
     fichero ajeno;
   - testing tiene que tener su propio `ACTIVITY_RESOURCES_ROOT`, distinto del de producción;
   - el servicio de testing tiene que ejecutarse con el mismo usuario que el de producción:
     los directorios de actividad se crean con el mismo dueño y modo que los de producción
     (`cp -a`), así que otro usuario podría leer los ficheros pero no subir ni borrar en
     ellos.

   Es lo que hace `scripts/copy-prod-to-testing.sh` (salvo con `--no-files`): con testing
   parado y antes del volcado de la base de datos, vacía el contenido del directorio de
   testing, sin borrar el directorio en sí, y lo rellena con `cp -al` desde el de
   producción. Antes de parar nada comprueba que testing tiene un directorio propio (no el
   mismo que el de producción, ni uno dentro del otro), que no está en la raíz del
   sistema de ficheros, que solo contiene directorios numéricos de actividad (si hay algo
   más, se niega a vaciarlo), que los dos están en el mismo sistema de ficheros y que se
   puede crear un hard-link. Mientras los dos entornos sigan compartiendo directorio, el
   script se niega a ejecutar este paso: cambia primero el `ACTIVITY_RESOURCES_ROOT` de
   testing, o usa `--no-files` para copiar solo la base de datos.

   Limitación: depende de que ningún código futuro modifique en sitio un fichero guardado
   (miniaturas, redimensionado de imágenes...), porque lo haría también en producción sin
   avisar. `Storage.Save` lo deja anotado en su comentario.

**Secuencias y orden de la copia (opciones 2 y 3).** Cada id de recurso sale de una
secuencia de PostgreSQL (`bigserial`), y una subida en testing solo puede chocar con un
fichero que ya exista en su árbol si la secuencia de testing llega a un id que ese árbol
ya tiene. Eso no se puede garantizar copiando en un orden u otro: depende de cómo se copie
la base de datos y de qué ids haya en cada lado. Lo que sí se puede es reducir la
probabilidad:

- si la base de datos se copia entera (`pg_dump` y restauración, que arrastran el valor de
  las secuencias), copia los ficheros *antes* del volcado, para que producción no pueda
  haber creado ninguno entre una cosa y la otra y dejado en el árbol un id por delante de
  la secuencia;
- si solo se copian las filas, hay que ajustar después la secuencia de testing con
  `setval`, al menos por encima del id más alto del árbol de ficheros.

Aun así, ni el orden ni el estado de las secuencias pueden corromper nada: `Save` nunca
pisa un fichero existente, así que lo peor que puede ocurrir es que una subida concreta en
testing falle con un error (un id nuevo que cae en un fichero que ya existe, por ejemplo
uno huérfano de producción), lo cual en testing es asumible.

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
