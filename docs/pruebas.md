# Pruebas

Este documento explica cómo ejecutar las pruebas y qué demuestra cada una. Incluye la trazabilidad de cada requisito funcional y cada invariante a las pruebas que lo cubren (sección 4).

## 1. Verificación completa con un solo comando

PowerShell:

~~~powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-p1.ps1
~~~

Linux, macOS o Git Bash:

~~~bash
sh ./scripts/verify-p1.sh
~~~

El comando ejecuta, en orden:

1. las pruebas unitarias (`go test ./...`);
2. el análisis estático (`go vet ./...`);
3. la validación del Compose;
4. el arranque de la pila completa;
5. todas las pruebas de integración de la sección 3.

Ninguno de los dos scripts elimina los volúmenes de PostgreSQL. Usan un límite de paralelismo de Compose de uno, para que también funcionen en máquinas con pocos recursos.

## 2. Pruebas unitarias y comprobaciones estáticas

~~~bash
go test ./...
go vet ./...
docker compose config --quiet
~~~

Validan el comportamiento de los paquetes, las reglas de autorización, la lógica de transacciones y del outbox, las reglas de RF14 y RF15, y el modelo de Compose. Las pruebas unitarias aíslan la lógica con dobles falsos (almacén, transacción y auditoría), así que no necesitan Docker ni datos.

**Pacientes (RF14), en `internal/agenda/patients_test.go`.** Cada prueba indica en un comentario `Cubre` el requisito o invariante que sostiene (RF14, RF18, I5). Verifican, entre otras cosas:

- que una entrada inválida se rechace antes de abrir la transacción;
- que crear, actualizar y leer escriban el evento de auditoría correspondiente;
- que la operación falle, y no devuelva el contacto, si el evento de auditoría no se puede guardar;
- que el listado no audite;
- que todas las operaciones usen la clínica del actor y que ninguna entrada tenga un campo de clínica.

**Historial, agenda del día y conteo por estado (RF16, RF20, RF21), en `internal/agenda/reports_test.go`.** Cada prueba indica en un comentario `Cubre` el requisito o invariante que sostiene. Verifican, entre otras cosas:

- que las fechas, el estado, el profesional, el rango y el id del paciente se validen antes de abrir la transacción, incluido el límite de 366 días;
- que el historial de un paciente que no existe en la clínica sea 404 sin consultar sus citas, que el límite por defecto sea 50 y el máximo 200, y que el profesional lo consulte igual que los demás roles;
- que la agenda sin profesional sea la de toda la clínica, y que un profesional pedido que no existe en la clínica sea 404;
- que un profesional reciba su propia agenda por defecto y no pueda ver la de otro (403);
- que las horas salgan en la zona de la clínica;
- que el paciente de la agenda no tenga campos de contacto;
- que el conteo devuelva siempre los cinco estados, con cero cuando no hay citas, y sume el total;
- que todas las consultas usen la clínica del actor, en transacciones de solo lectura;
- los permisos de cada rol sobre el historial, la agenda y el conteo.

**Horarios, disponibilidad, citas y caché (RF5 a RF13), en `internal/agenda/scheduling_test.go`, `appointments_test.go` y `availability_cache_test.go`.** Cada prueba indica en un comentario `Cubre` lo que sostiene. Verifican, entre otras cosas:

- el cálculo de los espacios a partir del horario, los bloqueos, las citas y la duración del servicio, en la zona horaria de la clínica;
- la máquina de estados de la cita: desde `booked` hacia los cuatro estados finales, y ninguna salida de un estado final;
- que una reserva fuera del horario, dentro de un bloqueo o con un profesional que no atiende el servicio se rechace sin insertar nada;
- que reprogramar hacia otro profesional bloquee las dos filas en orden de id y cierre la cita anterior antes de crear la nueva;
- que una invalidación deje huérfanas las entradas anteriores de la caché, y que una lectura vieja no pueda pisar una invalidación;
- que, si Redis no responde al invalidar, esa agenda deje de servirse desde la caché hasta que la invalidación se logre.

**Notas clínicas (RF15).** Verifican, entre otras cosas:

- que solo el profesional autorizado llegue al verificador de Agenda;
- que una cita no atendida sea rechazada;
- que una cita de otro profesional no se revele;
- que clínica, paciente y profesional no puedan ser inventados desde el cuerpo de la petición;
- que la escritura genere `clinical_note.write` y la lectura `clinical_note.read`;
- que la lectura auditada utilice una transacción de lectura y escritura;
- que el token original del usuario se reenvíe al adaptador de Agenda.

## 3. Pruebas de integración contra la pila real

Todas usan el Compose real: Keycloak emite tokens reales, cada servicio los valida y las peticiones pasan por el proxy.

### 3.1 Fronteras de privilegios de PostgreSQL

~~~bash
docker compose --profile test run --rm db-security-test
~~~

Verifica operaciones permitidas y prohibidas contra una instancia real:

- los perfiles `owner`, `app` y `readonly` no tienen atributos elevados (superusuario, creación de bases o de roles, replicación, omisión de RLS);
- el perfil `owner` solo puede hacer DDL en su propia base;
- los perfiles de aplicación no pueden hacer DDL ni crear tablas, ni siquiera temporales, ni asumir el rol del dueño;
- los perfiles de aplicación sí pueden hacer el DML que necesitan;
- los perfiles de solo lectura pueden consultar pero no escribir;
- las credenciales de un servicio no pueden conectarse a la base de otro;
- las tablas que solo admiten `SELECT` e `INSERT` conservan esos privilegios aunque el trabajo idempotente `db-init` se ejecute de nuevo;
- `audit_app` puede insertar y leer, pero no actualizar ni borrar;
- `expediente_app` no puede hacer `UPDATE`, `DELETE` ni `TRUNCATE` sobre `clinical_notes`.

El script termina con error en cuanto una operación prohibida tiene éxito. Última línea esperada: `All database privilege checks passed.`

### 3.2 Inmutabilidad e idempotencia de la auditoría (I6)

~~~bash
docker compose --profile test run --rm audit-invariants-test
~~~

Verifica que los outbox de los productores y la bitácora central no se pueden modificar con las identidades de la aplicación, y que reenviar un mismo evento produce una sola fila central. Última línea esperada: `All audit idempotency and immutability checks passed.`

### 3.3 Autenticación real, permisos y aislamiento entre clínicas

~~~bash
docker compose --profile test run --build --rm p1-api-integration-test
~~~

Verifica que:

- un token ausente o mal formado recibe 401;
- un usuario sin el permiso requerido recibe 403;
- un encabezado `X-Clinic-Id` o un parámetro `clinic_id` falsificados no sustituyen al `clinic_id` firmado del token;
- los servicios y usuarios de otra clínica se comportan como inexistentes (404), en Agenda y en Tenancy;
- las rutas de Expediente están restringidas a profesionales;
- nginx no expone las rutas `/internal`, ni siquiera a una cuenta de servicio válida.

Usa las clínicas Demo y Norte de la semilla. Última línea esperada: `All P1 API integration checks passed.`

### 3.4 Entrega real de los relays hacia Auditoría

~~~bash
docker compose --profile test run --rm audit-relay-test
~~~

Inserta eventos sintéticos en los outbox reales de Agenda y Expediente. Los relays en ejecución deben descubrir el evento pendiente, obtener su token de cuenta de servicio, llamar a la ruta privada de Auditoría, guardar el evento en `audit_log` y registrar la entrega por separado en `audit_outbox_delivered`. Los eventos sintéticos usan una clínica de prueba que no es una de las clínicas de la semilla, así que no aparecen en las demostraciones normales de RF19. Última línea esperada: `All audit relay delivery checks passed.`

### 3.5 Agenda: pacientes, historial, agenda del día, conteo por estado e I5

~~~bash
docker compose --profile test run --build --rm agenda-api-integration-test
~~~

Son 44 comprobaciones sobre la API (`cmd/agendacheck`): 23 de pacientes, 8 del historial y 13 de la agenda y el conteo. Cada una lleva en su nombre el requisito o invariante que sostiene. Se pueden repetir sin preparar datos, porque cada ejecución usa identificaciones únicas. Las de RF16, RF20 y RF21 se apoyan en las citas de `seed/agenda.sql`. Las de pacientes verifican:

- los permisos por rol (recepción y administración escriben, el profesional solo lee, la administración de plataforma no accede) y el 401 sin token;
- las validaciones, el conflicto por identificación repetida y que las tildes se conserven;
- que el `PATCH` cambie un dato, borre otro y conserve el resto;
- que otra clínica no vea ni modifique un paciente, ni con un `clinic_id` manipulado en el cuerpo o en la consulta;
- que el listado no exponga datos de contacto;
- que la bitácora central tenga **exactamente** los accesos al contacto que se hicieron, y que las operaciones rechazadas no dejen eventos;
- que la bitácora de otra clínica no muestre esos eventos.

Las del historial verifican:

- que traiga las citas del paciente de la más reciente a la más antigua, con su estado, y sin datos de contacto del paciente;
- que una cita cancelada traiga su motivo y que la que reprograma a otra apunte a la original;
- que un paciente sin citas devuelva una lista vacía, y que el límite devuelva las citas más recientes;
- que lo lean administración, recepción y profesionales (401 sin token, 403 para la administración de plataforma);
- las entradas inválidas (400), el 404 de un paciente inexistente y las horas de pared de la clínica (las 08:00 de Bogotá);
- que otra clínica no vea el historial de un paciente (404), ni con un `clinic_id` manipulado en los parámetros.

Las de la agenda y el conteo verifican:

- la agenda de toda la clínica y la de un profesional, con las citas de la semilla, ordenadas por hora y sin datos de contacto del paciente;
- el filtro por estado, y que el profesional vea solo su agenda (403 si pide la de otro);
- las horas de pared de cada clínica: las 08:00 de Costa Rica (`-06:00`) y las 08:00 de Bogotá (`-05:00`);
- que el conteo de cada clínica coincida con la semilla, que un rango de un solo día sea válido y que los estados sin citas salgan en cero;
- las entradas inválidas (400), el 401 sin token y el 403 de los roles que no corresponden;
- que otra clínica no vea la agenda ni los conteos, ni con un `practitioner_id` ajeno ni con un `clinic_id` manipulado en los parámetros.

Última línea esperada: `All Agenda integration checks passed.`

### 3.6 Fuga entre clínicas en los cuatro servicios (I5)

~~~bash
docker compose --profile test run --build --rm leak-integration-test
~~~

Son 12 comprobaciones (`cmd/leakcheck`) sobre Tenancy, Agenda, Expediente y Auditoría, con tokens reales de las tres clínicas de la semilla. Cada una intenta alcanzar datos de otra clínica por rutas, ids ajenos, cuerpos y filtros manipulados. Se apoyan en `seed/tenancy.sql`, `seed/agenda.sql` y `seed/expediente.sql`. Verifican:

- **un control:** que cada clínica lea sus propios recursos en los cuatro servicios (200). Sin él, un 404 en las demás pruebas podría significar que la ruta no existe;
- que las rutas de los cuatro servicios respondan 401 sin token y con un token falso;
- que las rutas `/internal/...` no se alcancen por el proxy (404), ni con el token de un administrador;
- **Tenancy:** que cada administrador liste solo a los usuarios de su clínica, que un alta con otra clínica en el cuerpo, la consulta y la cabecera quede en la del token, y que solo la plataforma liste las clínicas;
- **Agenda:** que los listados de servicios, profesionales y pacientes no traigan datos de otra clínica, y que un profesional de una clínica no pueda atender un servicio ni ligarse a un usuario de otra;
- **la matriz de las tres clínicas:** desde cada clínica, 12 intentos de leer o modificar los recursos de las otras dos (servicios, profesionales, pacientes, historial, agenda, usuarios y notas), cada uno repetido con un `clinic_id` y una cabecera `X-Clinic-Id` manipulados, y al final que los recursos atacados no hayan cambiado;
- **Expediente:** que solo el dueño lea su nota, y no otro profesional de la misma clínica, ni los de otras clínicas, ni administración, recepción o la plataforma;
- **Auditoría:** que cada administrador vea solo eventos de su clínica (esperando a que el relay entregue las lecturas de las notas), también con filtros de otra clínica, y que solo el administrador la consulte.

Se pueden repetir sin preparar datos. La prueba de alta crea un usuario con un nombre único por ejecución y lo da de baja al terminar, así que quedan usuarios inactivos con el prefijo `fuga-`. Última línea esperada: `All tenant isolation leak checks passed.`

### 3.7 Agenda: horarios, disponibilidad, citas, carrera y caché

~~~bash
docker compose --profile test run --build --rm citas-api-integration-test
~~~

Son 33 comprobaciones sobre la API (`cmd/citascheck`). Cada una lleva en su nombre el requisito o invariante que sostiene. No dependen de la semilla de horarios ni de citas: fijan el horario de dos profesionales de la clínica Demo, trabajan sobre fechas lejanas elegidas al azar y, al terminar, cancelan las citas que quedaron vigentes, borran sus bloqueos y restauran los horarios que había. Se pueden repetir sin preparar datos. Verifican:

- **el ciclo de la cita** (criterio de aceptación 2): reservar, reprogramar, cancelar y registrar el desenlace, con sus códigos, y el rechazo de toda transición desde un estado final;
- **los tres desenlaces de la reserva**: concedida (201), espacio no disponible (409, con el motivo) y solicitud inválida (400);
- **la carrera** (criterio 6): en cada una de 20 rondas queda un solo espacio libre, 12 clientes lo piden a la vez y debe haber exactamente un 201 y once 409. Todos los clientes esperan la misma señal antes de enviar, así que la coincidencia no depende de la suerte. Una segunda carrera reserva al mismo paciente con dos profesionales a la vez;
- **la caché** (criterio 5): consultar, reservar uno de los espacios devueltos y volver a consultar. La segunda respuesta no trae el espacio y no sale de una entrada anterior al cambio. Lo mismo al cancelar, al reprogramar y al cambiar los servicios del profesional;
- **los horarios y los bloqueos**: varios bloques por día, validaciones, la regla de que el profesional solo administra los propios, y que un bloqueo o un cambio de horario no cancelen citas sino que las listen como conflictos;
- **el aislamiento** (I5): otra clínica recibe 404 en las trece rutas de citas, horarios, bloqueos y disponibilidad, también con un `clinic_id` falsificado en el cuerpo o en la consulta;
- **RF15 de punta a punta**: Expediente acepta la nota de una cita atendida, rechaza la de una cita sin atender y no revela la cita de otro profesional.

Una prueba necesita una cita cuya hora de inicio ya pasó (RF12). Como no se puede reservar en el pasado, abre por un momento el horario a las 24 horas, reserva una cita que empieza en tres segundos y espera a que empiece.

Última línea esperada: `All appointment integration checks passed.`

## 4. Trazabilidad

Las pruebas de integración se identifican por el nombre que imprimen; las unitarias, por el nombre de la función. `citascheck`, `agendacheck`, `leakcheck` y `p1check` son los programas de `cmd/`.

### 4.1 Requisitos funcionales

| Requisito | Pruebas |
|---|---|
| RF1, RF2 | Unitarias de `internal/tenancy` (saga de alta, compensaciones, roles, baja). `p1check`: permisos por rol sobre clínicas y usuarios |
| RF3, RF4 | Unitarias de `internal/agenda/catalog_test.go`. `p1check`: permisos y aislamiento del catálogo |
| RF5 | `citascheck`: `RF5 el horario admite varios bloques por día…`, `RF5 los horarios inválidos se rechazan`, `RF5 permisos…`, `RF5 cambiar el horario lista las citas que quedan fuera`. Unitaria: `TestNormalizeSchedule` |
| RF6 | `citascheck`: `RF6 I3 un bloqueo retira la disponibilidad…`, `RF6 un bloqueo no cancela las citas reservadas…`, `RF6 permisos y entradas inválidas de los bloqueos` |
| RF7 | `citascheck`: `RF7 la disponibilidad sale del horario y de la duración del servicio`, `RF7 las consultas inválidas…`. Unitarias: `TestAvailableSlots…`, `TestScheduleIsInTheClinicTimeZone` |
| RF8 | `citascheck`: `RF8 disponibilidad por servicio en un rango de fechas` |
| RF9 | `citascheck`: `RF9 reservar concede la cita`, `RF9 I1 un espacio tomado o solapado responde 409`, `RF9 las solicitudes inválidas…`, `RF9 permisos…`. Unitarias: `TestBookGrantsAValidSlot`, `TestBookRejectsInvalidInput` |
| RF10 | `citascheck`: `RF10 I7 reprogramar libera el espacio anterior…`, `RF10 si el espacio nuevo no está libre…`, `RF10 reprogramar con un profesional que no atiende el servicio…`. Unitarias: `TestRescheduleLocksPractitionersInIDOrder`, `TestRescheduleRejections` |
| RF11 | `citascheck`: `RF11 cancelar exige motivo y registra quién canceló`, `RF11 I7 el espacio cancelado vuelve a estar disponible…` |
| RF12 | `citascheck`: `RF12 el desenlace no se registra antes de la hora de inicio`, `RF12 RF13 desenlace de una cita ya iniciada, una sola vez` |
| RF13 | `citascheck`: `RF13 un estado final no admite ninguna transición`. Unitaria: `TestCanTransition` |
| RF14 | `agendacheck` (sección 3.5) y `internal/agenda/patients_test.go` |
| RF15 | `citascheck`: `RF15 una nota clínica sobre una cita atendida se acepta de punta a punta`, `RF15 la ruta interna de contexto de cita no se expone por el proxy`. Unitarias de `internal/expediente` |
| RF16 | `agendacheck`: las siete pruebas `RF16 …` (historial ordenado, motivo de cancelación y reprogramación, historial vacío, límite, permisos, entradas inválidas y zona horaria) y `I5 el historial no cruza clínicas`. Unitarias: `TestHistory…` de `internal/agenda/reports_test.go` |
| RF17 | `p1check`: 401 sin token, 403 con un rol que no corresponde y 200 con el adecuado. Unitarias de `internal/platform/auth`, entre ellas `TestPermissionMatrix` |
| RF18 | `agendacheck`: `RF18 cada acceso al contacto queda en la bitácora, y solo esos`. Pruebas de invariantes de auditoría y de entrega de relays (secciones 3.2 y 3.4) |
| RF19 | `agendacheck`: consulta de la bitácora por paciente. `leakcheck`: `RF19 Auditoría: solo el administrador de la clínica la consulta`. Unitarias de `internal/audit` |
| RF20 | `agendacheck`: las seis pruebas `RF20 …` (toda la clínica, un profesional, filtro por estado, solo la agenda propia, entradas inválidas y zona horaria) y `RF17 la agenda exige token…`. Unitarias: `TestAgenda…` de `internal/agenda/reports_test.go` |
| RF21 | `agendacheck`: las tres pruebas `RF21 …` (conteo contra la semilla, rango y estados en cero, entradas inválidas) y `RF17 el conteo exige token…`. Unitarias: `TestCountByStatus…` |

### 4.2 Invariantes

| Invariante | Pruebas |
|---|---|
| I1 | `citascheck`: `I1 carrera: muchos clientes por el último espacio, una sola reserva concedida`; `RF9 I1 un espacio tomado o solapado responde 409` |
| I2 | `citascheck`: `I2 el mismo paciente no puede tener dos citas solapadas con otro profesional`; `I2 carrera: el mismo paciente con dos profesionales a la vez…` |
| I3 | `citascheck`: `I3 no se reserva fuera del horario de atención`; `RF6 I3 un bloqueo retira la disponibilidad y rechaza reservas`. Unitarias: `TestWithinSchedule`, `TestBookRejectsUnavailableSlots` |
| I4 | `citascheck`: `I4 la duración la fija el servicio, no la petición` |
| I5 | `citascheck`: `I5 otra clínica no ve ni modifica citas, horarios, bloqueos ni disponibilidad`, `I5 un clinic_id falsificado…`. `agendacheck`: las seis pruebas `I5` de pacientes y bitácora, y las de historial, agenda y conteo. `p1check`: aislamiento en Tenancy y en el catálogo. `leakcheck`: las nueve pruebas `I5` de los cuatro servicios, entre ellas la matriz de las tres clínicas |
| I6 | Sección 3.2 (`audit-invariants-test`) y sección 3.1 (`db-security-test`) |
| I7 | `citascheck`: `RF10 I7 reprogramar libera el espacio anterior y ocupa el nuevo`; `RF11 I7 el espacio cancelado vuelve a estar disponible de inmediato` |
| I8 | `citascheck`: `I8 la disponibilidad no ofrece un espacio recién reservado, aunque estuviera en caché`; `I8 un cambio en los servicios del profesional también invalida la caché`. Unitarias: `TestInvalidateOrphansPreviousEntries`, `TestStaleReadCannotOverwriteAnInvalidation`, `TestFailedInvalidationDisablesTheCacheForThatAgenda` |
