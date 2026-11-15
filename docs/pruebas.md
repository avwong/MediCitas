# Pruebas

Este documento explica cómo ejecutar las pruebas y qué demuestra cada una. Será la base del informe de pruebas, que además debe indicar qué prueba cubre cada requisito funcional y cada invariante (ver sección 4).

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

**Agenda del día y conteo por estado (RF20, RF21), en `internal/agenda/reports_test.go`.** Cada prueba indica en un comentario `Cubre` el requisito o invariante que sostiene. Verifican, entre otras cosas:

- que las fechas, el estado, el profesional y el rango se validen antes de abrir la transacción, incluido el límite de 366 días;
- que la agenda sin profesional sea la de toda la clínica, y que un profesional pedido que no existe en la clínica sea 404;
- que un profesional reciba su propia agenda por defecto y no pueda ver la de otro (403);
- que las horas salgan en la zona de la clínica;
- que el paciente de la agenda no tenga campos de contacto;
- que el conteo devuelva siempre los cinco estados, con cero cuando no hay citas, y sume el total;
- que todas las consultas usen la clínica del actor, en transacciones de solo lectura;
- los permisos de cada rol sobre la agenda y el conteo.

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

### 3.5 Agenda: pacientes, agenda del día, conteo por estado e I5

~~~bash
docker compose --profile test run --build --rm agenda-api-integration-test
~~~

Son 36 comprobaciones sobre la API (`cmd/agendacheck`): 23 de pacientes y 13 de la agenda y el conteo. Cada una lleva en su nombre el requisito o invariante que sostiene. Se pueden repetir sin preparar datos, porque cada ejecución usa identificaciones únicas. Las de RF20 y RF21 se apoyan en las citas de `seed/agenda.sql`. Las de pacientes verifican:

- los permisos por rol (recepción y administración escriben, el profesional solo lee, la administración de plataforma no accede) y el 401 sin token;
- las validaciones, el conflicto por identificación repetida y que las tildes se conserven;
- que el `PATCH` cambie un dato, borre otro y conserve el resto;
- que otra clínica no vea ni modifique un paciente, ni con un `clinic_id` manipulado en el cuerpo o en la consulta;
- que el listado no exponga datos de contacto;
- que la bitácora central tenga **exactamente** los accesos al contacto que se hicieron, y que las operaciones rechazadas no dejen eventos;
- que la bitácora de otra clínica no muestre esos eventos.

Las de la agenda y el conteo verifican:

- la agenda de toda la clínica y la de un profesional, con las citas de la semilla, ordenadas por hora y sin datos de contacto del paciente;
- el filtro por estado, y que el profesional vea solo su agenda (403 si pide la de otro);
- las horas de pared de cada clínica: las 08:00 de Costa Rica (`-06:00`) y las 08:00 de Bogotá (`-05:00`);
- que el conteo de cada clínica coincida con la semilla, que un rango de un solo día sea válido y que los estados sin citas salgan en cero;
- las entradas inválidas (400), el 401 sin token y el 403 de los roles que no corresponden;
- que otra clínica no vea la agenda ni los conteos, ni con un `practitioner_id` ajeno ni con un `clinic_id` manipulado en los parámetros.

Última línea esperada: `All Agenda integration checks passed.`

## 4. Trazabilidad

Pendiente. El informe de pruebas debe incluir una tabla que relacione cada requisito funcional y cada invariante con la prueba que lo cubre. Las pruebas de pacientes ya llevan esa información en sus nombres y comentarios.

Quedan por escribir, junto con los módulos que las necesitan, las pruebas de carrera, de caché y de fuga entre clínicas en los cuatro servicios.
