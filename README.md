# MediCitas

Plataforma SaaS multi-tenant de gestión de citas médicas. Una empresa la vende como servicio a muchas clínicas a la vez, y los datos de cada clínica permanecen estrictamente aislados.

El sistema se compone de cuatro servicios en Go detrás de un proxy, con PostgreSQL, Redis y Keycloak. El diseño completo, las alternativas consideradas y sus compromisos están en [`docs/arquitectura.md`](docs/arquitectura.md).

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
| `norte.admin` | `Norte123!` | `clinic_admin` | Clínica Norte |
| `norte.reception` | `Norte123!` | `reception` | Clínica Norte |

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
| `PATCH /api/v1/users/{id}/role` | `clinic_admin` | `role` | usuario | 200, 400, 404, 409 |
| `DELETE /api/v1/users/{id}` | `clinic_admin` | — | — (baja lógica) | 204, 404, 409 |

`role` acepta `clinic_admin`, `reception` o `practitioner`. El alta de una clínica usa una saga pequeña para mantener coordinados Tenancy, Keycloak y la proyección de Agenda.

### Agenda

Actualmente están implementados RF3 y RF4:

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

RF5–RF14, RF16, RF20 y RF21 pertenecen a Agenda y se incorporarán sobre esta misma frontera de servicio.

### Expediente

RF15 está implementado como dominio independiente y usa `notes:own`, permiso exclusivo del rol `practitioner`.

| Método y ruta | Rol | Entrada | Salida | Códigos |
|---|---|---|---|---|
| `POST /api/v1/clinical-notes` | `practitioner` | `appointment_id`, `content` | nota clínica | 201, 400, 404, 409, 503 |
| `GET /api/v1/clinical-notes/{id}` | `practitioner` | — | nota clínica propia | 200, 400, 404 |

La clínica y el profesional nunca se reciben en el body. Se toman del token autenticado. El paciente se obtiene del contexto verificado de la cita.

La escritura solo se acepta para una cita `attended` que haya sido atendida por ese mismo profesional y que todavía no tenga nota.

La lectura se filtra simultáneamente por clínica, id de nota y subject del profesional.

La creación y cada lectura escriben respectivamente `clinical_note.write` y `clinical_note.read` en `audit_outbox` dentro de la misma transacción de `expediente_db`.

El extremo de Expediente está listo; la creación end-to-end quedará habilitada cuando Agenda implemente su parte del contrato de citas descrito en [`docs/expediente.md`](docs/expediente.md).

### Auditoría

Auditoría centraliza los eventos de RF18 y expone RF19.

| Método y ruta | Rol | Entrada / filtros | Salida | Códigos |
|---|---|---|---|---|
| `GET /api/v1/audit` | `clinic_admin` | `user`, `patient_id`, `from`, `to`, `limit` opcionales | `{items: [...]}` | 200, 400, 401, 403 |

La clínica del filtro nunca viene de la petición: se deriva del `clinic_id` firmado del administrador.

Los eventos se entregan de forma asíncrona desde los outboxes de los servicios productores. La entrega es al menos una vez y Audit deduplica por `event_id`.

`audit_log` y los `audit_outbox` están protegidos contra UPDATE y DELETE en PostgreSQL. Las identidades runtime tampoco reciben permisos de mutación innecesarios sobre esas tablas.

### Rutas internas

Las rutas `/internal/...` solo existen dentro de la red de Compose. nginx responde 404 si un cliente externo intenta usarlas.

Agenda actualmente expone:

- `PUT /internal/clinics/{id}`
- `DELETE /internal/clinics/{id}`

Tenancy utiliza esas rutas con su cuenta de servicio para aprovisionar clínicas.

Expediente queda preparado para consumir, cuando el módulo de citas exista:

- `GET /internal/appointments/{id}/clinical-note-context`

Esa llamada reenviará **el token original del profesional**, no un `clinic_id` en headers ni parámetros. Agenda deberá validar el token y devolver únicamente el contexto de una cita visible para esa clínica.

El contrato esperado está documentado en [`docs/expediente.md`](docs/expediente.md).

Audit recibe eventos de sus productores mediante:

- `POST /internal/audit/events`

Agenda y Expediente utilizan cuentas de servicio independientes y OAuth 2.0 client credentials para ese relay.

## Auditoría transaccional

Agenda y Expediente disponen de `audit_outbox` append-only.

La operación sensible y el evento se escriben en la misma transacción local. El relay no modifica el outbox: registra los eventos entregados en `audit_outbox_delivered`.

Si Audit está temporalmente fuera de servicio, el dominio puede conservar el evento localmente y reintentarlo.

Audit recibe los reenvíos de forma idempotente mediante `event_id`, por lo que una entrega repetida produce una sola fila central.

El contenido de una nota clínica y los valores de contacto de un paciente nunca se copian al evento de auditoría.

## Bases de datos y menor privilegio

Cada servicio posee tres perfiles PostgreSQL:

- `<service>_owner`: migraciones y DDL;
- `<service>_app`: identidad runtime;
- `<service>_readonly`: diagnóstico SELECT-only.

Los roles no son superusuarios, no pueden crear roles ni bases, no pueden hacer bypass de RLS y no pueden conectarse a las bases de los demás servicios.

El rol runtime de Audit está limitado a las operaciones necesarias para una bitácora append-only.

Expediente restringe además `clinical_notes` a `SELECT` e `INSERT` para su rol runtime porque RF15 no define actualización ni borrado de notas.

## Pruebas

Pruebas Go:

~~~bash
go test ./...
go vet ./...
~~~

Validación del Compose:

~~~bash
docker compose config --quiet
~~~

Fronteras de privilegios PostgreSQL:

~~~bash
docker compose --profile test run --rm db-security-test
~~~

Inmutabilidad e idempotencia de auditoría:

~~~bash
docker compose --profile test run --rm audit-invariants-test
~~~

Las pruebas unitarias cubren actualmente autenticación/autorización, mapeo de errores, saga de clínicas, catálogo, outbox, Auditoría y el núcleo de Expediente RF15.

Las pruebas RF15 verifican, entre otras cosas:

- que solo el profesional autorizado llegue al verificador de Agenda;
- que una cita no atendida sea rechazada;
- que una cita de otro profesional no se revele;
- que clínica, paciente y profesional no puedan ser inventados desde el body;
- que la escritura genere `clinical_note.write`;
- que la lectura genere `clinical_note.read`;
- que la lectura auditada utilice una transacción read-write;
- que el token original del usuario se reenvíe al adaptador de Agenda.

Las pruebas completas de carrera, caché y fuga de datos de los requisitos de Agenda se añadirán con esos módulos.

## Estructura

~~~
cmd/<servicio>/          binario de cada servicio
internal/platform/       auth, DB/TxManager, HTTP, outbox, clientes internos
internal/tenancy/        RF1 y RF2
internal/agenda/         catálogo y futuros módulos de Agenda
internal/expediente/     RF15
internal/audit/          RF19 y almacén RF18
migrations/<servicio>/   migraciones por base de datos
seed/                    datos de prueba
keycloak/                realm, roles y clients
deploy/                  PostgreSQL, pruebas de infraestructura y proxy
docs/                    arquitectura y contratos entre servicios
~~~

## Estado

| Implementado | Pendiente / integración futura |
|---|---|
| RF1, RF2, RF3, RF4, RF17 y RF19 | RF5–RF14, RF16, RF20 y RF21 |
| Núcleo RF15: BD, autorización, API, aislamiento lógico, auditoría read/write y contrato hacia Agenda | Endpoint de contexto de cita en Agenda para completar RF15 end-to-end |
| Infraestructura RF18: outbox transaccional, relay, bitácora central idempotente e inmutable | Productor RF18 de contacto de pacientes cuando RF14 sea implementado |
| Bases por servicio, owner/app/readonly y aislamiento de credenciales | RLS y roles de dominio alineados con la matriz, según el módulo del equipo |
| JWT/OIDC, permisos, signed `clinic_id`, saga RF1 y clientes service-to-service | Disponibilidad, concurrencia de citas, caché Redis y pruebas de carrera |
| Tests reales de privilegios e invariantes de auditoría | Suite de integración completa contra todos los RF restantes |
