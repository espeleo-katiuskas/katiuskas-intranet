# Scripts

Herramientas de línea de comandos del proyecto. Cada script explica su uso y sus opciones
en el comentario del principio del propio fichero; `generate-license-thumbnails.sh`
también con `--help`, y `get-token.sh` lo muestra si se ejecuta mal.

| Script | Qué hace | Dónde se usa |
| --- | --- | --- |
| `get-token.sh` | Crea directamente en la base de datos un token de sesión para una persona (por login, o buscándola por correo o nombre con `--user`) y lo imprime, para probar la API con `curl` o entrar en la web sin pasar por el enlace de acceso. | Solo desarrollo local, con el `docker compose` levantado. **Nunca en producción.** |
| `copy-prod-to-testing.sh` | Sustituye la base de datos de testing por una copia de la de producción, la migra hasta el esquema que espera el código de testing y enlaza (hard links) los ficheros de las memorias de actividad de producción. Solo lee producción; todo lo que hubiera en testing se pierde. | El host del club, en cada despliegue a producción. Ver `docs/environments.md`. |
| `generate-license-thumbnails.sh` | Crea las miniaturas que falten (o estén anticuadas) de las copias de licencias federativas que hay en `user-files/<login>/licencias/`: la primera página de un PDF, o una versión reducida de una imagen. | Cuando se añaden copias de licencias, tanto en local como en producción. Ver `docs/deployment-no-docker.md`. |

## Ejemplos

```sh
scripts/get-token.sh admin.club
scripts/get-token.sh --user garcia

TESTING_SERVICE=<unidad de systemd de testing> \
  scripts/copy-prod-to-testing.sh [--yes] [--no-files] <api.env de producción> <api.env de testing>

scripts/generate-license-thumbnails.sh --dry-run
```
