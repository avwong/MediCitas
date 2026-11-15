# MediCitas

Plataforma SaaS multi-tenant de gestión de citas médicas. Una empresa la vende como servicio a muchas clínicas a la vez, y los datos de cada clínica permanecen estrictamente aislados.

El sistema se compone de cuatro servicios en Go detrás de un proxy, con PostgreSQL, Redis y Keycloak. El diseño completo, las alternativas consideradas y sus compromisos están en [`docs/design.md`](docs/design.md).

| Servicio | Responsabilidad | Requisitos | Base de datos |
|---|---|---|---|
| `tenancy` | Clínicas y cuentas de usuario; único con acceso a la API de administración de Keycloak | RF1, RF2 | `tenancy_db` |
| `agenda` | Catálogo, profesionales, horarios, bloqueos, pacientes, disponibilidad y citas | RF3–RF14, RF16, RF20, RF21 | `agenda_db` |
| `expediente` | Notas clínicas aisladas del resto del dominio | RF15 y productor RF18 | `expediente_db` |
| `audit` | Bitácora central append-only y consulta administrativa | RF18, RF19 | `audit_db` |

Keycloak es la fuente de verdad de la identidad: el rol y la clínica de cada usuario viajan firmados en el token, y **cada servicio valida el token por su cuenta**. El proxy (nginx) solo enruta.

## Arranque

Requisitos: Docker con Compose v2.

~~~bash
cp .env.example .env
docker compose up --build
~~~

Un solo comando levanta la pila: crea una base de datos y tres perfiles PostgreSQL de menor privilegio por servicio (`owner`, `app` y `readonly`), aplica las migraciones, carga los datos semilla e importa el realm de Keycloak con sus roles, clients y usuarios de prueba. Los servicios esperan a que la base y Keycloak estén listos antes de arrancar.

`audit_app` es todavía más restrictivo: la bitácora central solo necesita `SELECT` e `INSERT`.

| Componente | URL en el host |
|---|---|
| API (proxy) | http://localhost:8080 |
| Keycloak | http://localhost:8081 (consola: `admin` / `admin_dev`) |

Para empezar desde cero deliberadamente, eliminando también el volumen de desarrollo:

~~~bash
docker compose down -v
docker compose up --build
~~~

Esto también es lo más simple si, después de actualizar el repositorio, una migración o la semilla fallan sobre un volumen creado con una versión anterior del esquema.

## Autenticación

Los tokens se piden a Keycloak con el client público `medicitas-cli`:

~~~bash
TOKEN=$(curl -s http://localhost:8081/realms/medicitas/protocol/openid-connect/token \
  -d grant_type=password -d client_id=medicitas-cli \
  -d username=clinic.admin -d password='Clinic123!' | jq -r .access_token)

curl -s http://localhost:8080/api/v1/me -H "Authorization: Bearer $TOKEN"
~~~

Usuarios de prueba:

| Usuario | Contraseña | Rol | Clínica |
|---|---|---|---|
| `platform.admin` | `Platform123!` | `platform_admin` | — |
| `clinic.admin` | `Clinic123!` | `clinic_admin` | Clínica Demo |
| `reception` | `Reception123!` | `reception` | Clínica Demo |
| `practitioner` | `Practitioner123!` | `practitioner` | Clínica Demo |
| `practitioner2` | `Practitioner123!` | `practitioner` | Clínica Demo |
| `practitioner3` | `Practitioner123!` | `practitioner` | Clínica Demo |
| `norte.admin` | `Norte123!` | `clinic_admin` | Clínica Norte |
| `norte.reception` | `Norte123!` | `reception` | Clínica Norte |
| `norte.practitioner1`, `norte.practitioner2`, `norte.practitioner3` | `Norte123!` | `practitioner` | Clínica Norte |
| `sur.admin` | `Sur123!` | `clinic_admin` | Clínica Sur |
| `sur.reception` | `Sur123!` | `reception` | Clínica Sur |
| `sur.practitioner1`, `sur.practitioner2`, `sur.practitioner3` | `Sur123!` | `practitioner` | Clínica Sur |

La semilla crea tres clínicas (Demo, Norte y Sur). Cada una tiene su administrador, su recepción y tres profesionales, y cada profesional atiende los servicios de su especialidad. Los profesionales de Agenda coinciden con usuarios de Tenancy y de Keycloak. La Clínica Sur usa la zona horaria `America/Bogota`, para probar que cada clínica respeta la suya.

Reglas que aplican todos los servicios:

- Sin token, o con un token inválido o vencido: **401**.
- Con un token válido pero sin el permiso requerido: **403**.
- La clínica **siempre** se toma del claim firmado `clinic_id`, nunca de la petición.
- Un recurso de otra clínica se trata como inexistente para no revelar información entre tenants.
- Los tokens duran 5 minutos: una baja o un cambio de rol surte efecto, como máximo, en ese plazo. Keycloak bloquea los inicios de sesión nuevos al instante.
- Cada servicio valida localmente firma, issuer, expiración y audience; nginx no autentica.

## Contrato de la API

Todas las rutas reciben y devuelven JSON. Los errores tienen la forma `{"error": "mensaje"}`. Códigos comunes a todas las rutas protegidas: `401` sin token válido, `403` sin permiso, `400` con un cuerpo o identificador mal formado.

### Tenancy

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `GET /api/v1/me` | cualquiera | — | identidad del token y registro local | 200 |
| `POST /api/v1/platform/clinics` | `platform_admin` | `identifier`, `name`, `timezone`, `admin{username, email, first_name, last_name, password}` | `{clinic, admin}` | 201, 400, 409, 503 |
| `GET /api/v1/platform/clinics` | `platform_admin` | — | `{items: [clinic]}` | 200 |
| `POST /api/v1/users` | `clinic_admin` | `username`, `email`, `first_name`, `last_name`, `password`, `role` | usuario | 201, 400, 409, 503 |
| `GET /api/v1/users` | `clinic_admin` | — | `{items: [usuario]}` | 200 |
| `GET /api/v1/users/{id}` | `clinic_admin` | — | usuario | 200, 404 |
| `PATCH /api/v1/users/{id}/role` | `clinic_admin` | `role` | usuario | 200, 400, 404, 409, 503 |
| `DELETE /api/v1/users/{id}` | `clinic_admin` | — | — (baja lógica) | 204, 404, 409, 503 |

`role` acepta `clinic_admin`, `reception` o `practitioner`. Cuando un usuario deja de ser `practitioner`, por cambio de rol o por baja, Tenancy desactiva a su profesional en Agenda, y lo reactiva si vuelve a tener ese rol. Si Agenda no responde, la operación devuelve 503 y se puede repetir. El alta de una clínica usa una saga pequeña para mantener coordinados Tenancy, Keycloak y la proyección de Agenda.

### Agenda

Actualmente están implementados RF3, RF4 y RF14. Catálogo y profesionales (RF3 y RF4):

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `POST /api/v1/services` | `clinic_admin` | `name`, `duration_minutes` (1–480), `specialty` | servicio | 201, 400, 409 |
| `GET /api/v1/services` | personal de la clínica | — | `{items: [servicio]}` | 200 |
| `GET /api/v1/services/{id}` | personal de la clínica | — | servicio | 200, 404 |
| `DELETE /api/v1/services/{id}` | `clinic_admin` | — | — (baja lógica) | 204, 404 |
| `POST /api/v1/practitioners` | `clinic_admin` | `user_id`, `display_name`, `specialty`, `service_ids` | profesional | 201, 400, 404, 409 |
| `GET /api/v1/practitioners` | personal de la clínica | — | `{items: [profesional]}` | 200 |
| `GET /api/v1/practitioners/{id}` | personal de la clínica | — | profesional | 200, 404 |
| `PUT /api/v1/practitioners/{id}/services` | `clinic_admin` | `service_ids` | profesional | 200, 400, 404 |

Un profesional solo puede atender servicios activos de su clínica que requieran su especialidad; si algún servicio no cumple, la operación completa se rechaza.

La especialidad (`specialty`) se recibe como texto y se guarda en la tabla `specialties` de la clínica, sin distinguir mayúsculas: "Medicina general" y "medicina general" son la misma. Las respuestas incluyen `specialty` y `specialty_id`.

Pacientes (RF14):

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `POST /api/v1/patients` | `clinic_admin`, `reception` | `identification`, `first_name`, `last_name`, `birth_date` (`AAAA-MM-DD`); opcionales `phone`, `email`, `address` | paciente | 201, 400, 409 |
| `PATCH /api/v1/patients/{id}` | `clinic_admin`, `reception` | cualquiera de los campos anteriores | paciente | 200, 400, 404, 409 |
| `GET /api/v1/patients` | personal de la clínica | `q` (búsqueda por prefijo) y `limit` (1–200, por defecto 50), opcionales | `{items: [resumen]}` | 200, 400 |
| `GET /api/v1/patients/{id}` | personal de la clínica | — | paciente | 200, 400, 404 |

Reglas de los pacientes:

- Recepción y administración escriben; el profesional solo lee (matriz de permisos).
- En `PATCH`, un campo ausente no se modifica y un dato de contacto enviado vacío se borra. Un cuerpo sin ningún campo responde 400.
- La identificación es única dentro de la clínica (409 si se repite) y puede repetirse entre clínicas.
- `birth_date` no puede ser futura ni anterior a 1900-01-01. `phone` admite dígitos, espacios, `+`, `-` y paréntesis. `email` debe ser una dirección válida.
- La clínica nunca viaja en la petición: se toma del `clinic_id` firmado. Un paciente de otra clínica responde 404.
- El listado devuelve un resumen **sin datos de contacto** (`id`, `identification`, `first_name`, `last_name`, `birth_date`, `active`), por lo que no se audita.
- Cada creación, actualización y lectura de un paciente con sus datos de contacto escribe `patient_contact.write` o `patient_contact.read` en `audit_outbox`, dentro de la misma transacción. Si el evento no se puede guardar, la operación no se completa. El evento lleva solo identificadores, nunca los valores de contacto.

La semilla incluye 13 pacientes de ejemplo: 6 en Clínica Demo, 3 en Clínica Norte y 4 en Clínica Sur.

RF5–RF13, RF16, RF20 y RF21 pertenecen a Agenda y se incorporarán sobre esta misma frontera de servicio.

### Expediente

RF15 está implementado como dominio independiente y usa `notes:own`, permiso exclusivo del rol `practitioner`.

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `POST /api/v1/clinical-notes` | `practitioner` | `appointment_id`, `content` | nota clínica | 201, 400, 404, 409, 503 |
| `GET /api/v1/clinical-notes/{id}` | `practitioner` | — | nota clínica propia | 200, 400, 404 |

La clínica y el profesional nunca se reciben en el cuerpo de la petición. Se toman del token autenticado. El paciente se obtiene del contexto verificado de la cita.

La escritura solo se acepta para una cita `attended` que haya sido atendida por ese mismo profesional y que todavía no tenga nota.

La lectura se filtra simultáneamente por clínica, id de nota y subject del profesional.

La creación y cada lectura escriben respectivamente `clinical_note.write` y `clinical_note.read` en `audit_outbox` dentro de la misma transacción de `expediente_db`.

El extremo de Expediente está listo; la creación de punta a punta quedará habilitada cuando Agenda implemente su parte del contrato descrito en «Rutas internas».

### Auditoría

Auditoría centraliza los eventos de RF18 y expone RF19.

| Método y ruta | Rol | Entrada / filtros | Salida | Códigos |
|---|---|---|---|---|
| `GET /api/v1/audit` | `clinic_admin` | opcionales: `user` (subject o nombre de usuario), `patient_id`, `from` y `to` (RFC 3339, inclusivos), `limit` (1–500, por defecto 100) | `{items: [...]}` | 200, 400, 401, 403 |

La clínica del filtro nunca viene de la petición: se deriva del `clinic_id` firmado del administrador.

Los eventos se entregan de forma asíncrona desde los outboxes de los servicios productores. La entrega es al menos una vez y Audit deduplica por `event_id`.

`audit_log` y los `audit_outbox` están protegidos contra UPDATE y DELETE en PostgreSQL. Las identidades con las que corren los servicios tampoco reciben permisos de mutación innecesarios sobre esas tablas.

### Rutas internas

Las rutas `/internal/...` solo existen dentro de la red de Compose. nginx responde 404 si un cliente externo intenta usarlas.

Agenda actualmente expone:

- `PUT /internal/clinics/{id}`
- `DELETE /internal/clinics/{id}`
- `PUT /internal/practitioners/by-user/{user_id}/active`, con el cuerpo `{"active": true|false}`

Tenancy utiliza esas rutas con su cuenta de servicio: las dos primeras para aprovisionar clínicas y la tercera para que el profesional de Agenda refleje el estado de su usuario. Es idempotente y responde 204 aunque el usuario no tenga profesional registrado.

Expediente queda preparado para consumir, cuando el módulo de citas exista:

- `GET /internal/appointments/{id}/clinical-note-context`

Esa llamada reenviará **el token original del profesional**, no un `clinic_id` en encabezados ni parámetros. Agenda deberá validar el token y devolver únicamente el contexto de una cita visible para esa clínica.

Respuesta esperada, con el estado `attended` para una cita atendida:

~~~json
{
  "appointment_id": "uuid",
  "clinic_id": "uuid",
  "patient_id": "uuid",
  "practitioner_subject": "keycloak-subject",
  "status": "attended"
}
~~~

Reglas del contrato:

- Agenda deriva la clínica del token autenticado, nunca de parámetros ni de encabezados de quien llama.
- Solo devuelve una cita visible para esa clínica.
- `practitioner_subject` es el subject de Keycloak del profesional que atendió la cita.
- Agenda puede implementar sus tablas de citas como prefiera mientras se respete este contrato HTTP.

Expediente acepta la nota solo si:

1. quien llama tiene `notes:own`;
2. la cita existe en la clínica de quien llama;
3. Agenda informa la cita como `attended`;
4. la cita la atendió el subject de quien llama;
5. la cita todavía no tiene nota (hay una nota por cita).

La cita de otro profesional se responde como inexistente, para no revelar que existe.

Audit recibe eventos de sus productores mediante:

- `POST /internal/audit/events`

Agenda y Expediente utilizan cuentas de servicio independientes y OAuth 2.0 client credentials para ese relay.

## Pruebas

Las pruebas unitarias y el análisis estático necesitan Go 1.27 o superior instalado; las de integración solo necesitan Docker.

Verificación completa con un solo comando. Ejecuta las pruebas unitarias, el análisis estático y todas las pruebas de integración contra la pila real, y no borra los volúmenes:

~~~powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-p1.ps1
~~~

~~~bash
sh ./scripts/verify-p1.sh
~~~

Por separado:

~~~bash
go test ./...
go vet ./...
docker compose config --quiet
docker compose --profile test run --rm db-security-test
docker compose --profile test run --rm audit-invariants-test
docker compose --profile test run --build --rm p1-api-integration-test
docker compose --profile test run --rm audit-relay-test
docker compose --profile test run --build --rm agenda-api-integration-test
~~~

En orden, estos comandos comprueban: las pruebas unitarias, el análisis estático, el Compose, los privilegios de PostgreSQL, la inmutabilidad de la auditoría, la autenticación y el aislamiento entre clínicas, la entrega de los relays y los pacientes con su auditoría. Lo que demuestra cada una está en [`docs/pruebas.md`](docs/pruebas.md).

## Estructura

~~~
cmd/<servicio>/          binario de cada servicio
internal/platform/       auth, DB/TxManager, HTTP, outbox, clientes internos
internal/tenancy/        RF1 y RF2
internal/agenda/         catálogo, pacientes y futuros módulos de Agenda
internal/expediente/     RF15
internal/audit/          RF19 y almacén RF18
migrations/<servicio>/   migraciones por base de datos
seed/                    datos de prueba
keycloak/                realm, roles y clients
deploy/                  PostgreSQL, pruebas de infraestructura y proxy
docs/                    documento de diseño y documento de pruebas
~~~

## Estado

| Implementado | Pendiente / integración futura |
|---|---|
| RF1, RF2, RF3, RF4, RF14, RF17 y RF19 | RF5–RF13, RF16, RF20 y RF21 |
| Núcleo RF15: BD, autorización, API, aislamiento lógico, auditoría de lectura y escritura y contrato hacia Agenda | Ruta de contexto de cita en Agenda para completar RF15 de punta a punta |
| Infraestructura RF18: outbox transaccional, relay, bitácora central idempotente e inmutable, y productor de contacto de pacientes en Agenda, con pruebas de integración | — |
| Bases por servicio, owner/app/readonly y aislamiento de credenciales | RLS y roles de dominio alineados con la matriz, según el módulo del equipo |
| JWT/OIDC, permisos, `clinic_id` firmado, saga RF1 y clientes entre servicios | Disponibilidad, concurrencia de citas, caché Redis y pruebas de carrera |
| Pruebas reales de privilegios, invariantes de auditoría, JWT/proxy, aislamiento multi-tenant y entrega de relays | Suite de integración de los RF restantes de Agenda |
