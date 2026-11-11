# MediCitas

Plataforma SaaS multi-tenant de gestión de citas médicas. Una empresa la vende como servicio a muchas clínicas a la vez, y los datos de cada clínica permanecen estrictamente aislados.

El sistema se compone de cuatro servicios en Go detrás de un proxy, con PostgreSQL, Redis y Keycloak. El diseño completo, las alternativas consideradas y sus compromisos están en [`docs/arquitectura.md`](docs/arquitectura.md).

| Servicio | Responsabilidad | Requisitos | Base de datos |
|---|---|---|---|
| `tenancy` | Clínicas y cuentas de usuario; único con acceso a la API de administración de Keycloak | RF1, RF2 | `tenancy_db` |
| `agenda` | Catálogo, profesionales, horarios, bloqueos, pacientes, disponibilidad y citas | RF3–RF14, RF16, RF20, RF21 | `agenda_db` |
| `expediente` | Notas clínicas | RF15 | `expediente_db` |
| `audit` | Bitácora append-only | RF18, RF19 | `audit_db` |

Keycloak es la fuente de verdad de la identidad: el rol y la clínica de cada usuario viajan firmados en el token, y **cada servicio valida el token por su cuenta**. El proxy (nginx) solo enruta.

## Arranque

Requisitos: Docker con Compose v2.

```bash
cp .env.example .env
docker compose up --build
```

Un solo comando levanta todo: crea una base de datos y dos roles por servicio, aplica las migraciones, carga los datos semilla e importa el realm de Keycloak con sus roles, clients y usuarios de prueba. Los servicios esperan a que la base y Keycloak estén listos antes de arrancar.

| Componente | URL en el host |
|---|---|
| API (proxy) | http://localhost:8080 |
| Keycloak | http://localhost:8081 (consola: `admin` / `admin_dev`) |

Para empezar desde cero (por ejemplo, después de cambiar migraciones o semillas):

```bash
docker compose down -v
docker compose up --build
```

## Autenticación

Los tokens se piden a Keycloak con el client público `medicitas-cli`:

```bash
TOKEN=$(curl -s http://localhost:8081/realms/medicitas/protocol/openid-connect/token \
  -d grant_type=password -d client_id=medicitas-cli \
  -d username=clinic.admin -d password='Clinic123!' | jq -r .access_token)

curl -s http://localhost:8080/api/v1/me -H "Authorization: Bearer $TOKEN"
```

Usuarios de prueba:

| Usuario | Contraseña | Rol | Clínica |
|---|---|---|---|
| `platform.admin` | `Platform123!` | `platform_admin` | — |
| `clinic.admin` | `Clinic123!` | `clinic_admin` | Clínica Demo |
| `reception` | `Reception123!` | `reception` | Clínica Demo |
| `practitioner` | `Practitioner123!` | `practitioner` | Clínica Demo |
| `norte.admin` | `Norte123!` | `clinic_admin` | Clínica Norte |
| `norte.reception` | `Norte123!` | `reception` | Clínica Norte |

Reglas que aplican todos los servicios:

- Sin token, o con un token inválido o vencido: **401**.
- Con un token válido pero sin el rol requerido: **403**.
- La clínica **siempre** se toma del claim `clinic_id` del token, nunca de la petición. Un recurso de otra clínica responde **404**, igual que uno inexistente.
- Los tokens duran 5 minutos: una baja o un cambio de rol surte efecto, como máximo, en ese plazo. Keycloak bloquea los inicios de sesión nuevos al instante.

## Contrato de la API

Todas las rutas reciben y devuelven JSON. Los errores tienen la forma `{"error": "mensaje"}`. Códigos comunes a todas las rutas protegidas: `401` sin token válido, `403` sin permiso, `400` con un cuerpo o un identificador mal formado.

### Tenancy

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `GET /api/v1/me` | cualquiera | — | identidad del token y registro local | 200 |
| `POST /api/v1/platform/clinics` | `platform_admin` | `identifier`, `name`, `timezone`, `admin{username, email, first_name, last_name, password}` | `{clinic, admin}` | 201, 400, 409 (identificador o usuario existente), 503 |
| `GET /api/v1/platform/clinics` | `platform_admin` | — | `{items: [clinic]}` | 200 |
| `POST /api/v1/users` | `clinic_admin` | `username`, `email`, `first_name`, `last_name`, `password`, `role` | usuario | 201, 400, 409, 503 |
| `GET /api/v1/users` | `clinic_admin` | — | `{items: [usuario]}` | 200 |
| `GET /api/v1/users/{id}` | `clinic_admin` | — | usuario | 200, 404 |
| `PATCH /api/v1/users/{id}/role` | `clinic_admin` | `role` | usuario | 200, 400, 404, 409 (propia cuenta o usuario inactivo) |
| `DELETE /api/v1/users/{id}` | `clinic_admin` | — | — (baja lógica) | 204, 404, 409 |

`role` acepta `clinic_admin`, `reception` o `practitioner`. El alta de una clínica crea su primer administrador en Keycloak; si algún paso falla, se deshacen los anteriores.

### Agenda

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `POST /api/v1/services` | `clinic_admin` | `name`, `duration_minutes` (1–480), `specialty` | servicio | 201, 400, 409 (nombre repetido) |
| `GET /api/v1/services` | personal de la clínica | — | `{items: [servicio]}` | 200 |
| `GET /api/v1/services/{id}` | personal de la clínica | — | servicio | 200, 404 |
| `DELETE /api/v1/services/{id}` | `clinic_admin` | — | — (baja lógica) | 204, 404 |
| `POST /api/v1/practitioners` | `clinic_admin` | `user_id`, `display_name`, `specialty`, `service_ids` | profesional | 201, 400, 404 (usuario), 409 (no es profesional activo o ya registrado) |
| `GET /api/v1/practitioners` | personal de la clínica | — | `{items: [profesional]}` | 200 |
| `GET /api/v1/practitioners/{id}` | personal de la clínica | — | profesional | 200, 404 |
| `PUT /api/v1/practitioners/{id}/services` | `clinic_admin` | `service_ids` | profesional | 200, 400, 404 |

Un profesional solo puede atender servicios activos de su clínica que requieran su especialidad; si algún servicio no cumple, la operación completa se rechaza con 400.

### Rutas internas

Las rutas `/internal/...` solo existen dentro de la red de Compose (el proxy responde 404) y exigen el token de la cuenta de servicio correspondiente. Hoy Agenda expone `PUT` y `DELETE /internal/clinics/{id}`, que Tenancy usa para aprovisionar las clínicas.

## Pruebas

```bash
go test ./...
```

Ejecuta las pruebas unitarias: autorización contra la matriz de permisos, construcción de la identidad a partir del token, mapeo de errores, la saga de alta de clínica y las validaciones del catálogo. Las pruebas de integración contra la pila de Compose (aislamiento, carrera, caché) están pendientes.

## Estructura

```
cmd/<servicio>/          binario de cada servicio
internal/platform/       código compartido: auth, base de datos, HTTP, configuración
internal/<servicio>/     dominio, handlers y acceso a datos de cada servicio
migrations/<servicio>/   migraciones, ejecutadas con el rol dueño de cada base
seed/                    datos de prueba
keycloak/                realm importado al arrancar
deploy/                  script de bases y roles; configuración del proxy
docs/                    diseño y decisiones
```

## Estado

| Implementado | Pendiente |
|---|---|
| RF1, RF2, RF3, RF4, RF17 | RF5–RF16, RF18–RF21 |
| Base de datos y roles por servicio, aislamiento con FK compuestas | Pruebas de integración, de aislamiento y de carrera |
| Validación de tokens, permisos según la matriz, saga de alta de clínica | Caché de disponibilidad en Redis |
