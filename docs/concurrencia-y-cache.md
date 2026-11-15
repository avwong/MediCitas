# Concurrencia y caché

Este documento explica cómo Agenda reserva, reprograma y cancela citas cuando varias operaciones ocurren a la vez, y cómo sirve la disponibilidad desde Redis sin contradecir a la base. Cubre el módulo de transacciones y concurrencia y el de caché de disponibilidad del enunciado, y los invariantes I1, I2, I3, I4, I7 e I8.

El código está en `internal/agenda`: `appointments.go` (casos de uso), `scheduling_store.go` (SQL), `scheduling.go` (cálculo de espacios) y `availability_cache.go` (caché). El esquema está en las migraciones `0005` a `0007` de Agenda.

## 1. La pregunta rectora

> Dos recepcionistas reservan el último espacio de la agenda exactamente al mismo tiempo. ¿Qué hace el sistema, por qué, y cómo se demuestra?

**Qué hace.** Una reserva recibe `201` con la cita creada. La otra recibe `409` con el mensaje `slot already taken`. Nunca quedan dos citas, y ninguna de las dos peticiones queda sin respuesta.

**Por qué.** Hay dos mecanismos, y cualquiera de los dos basta por sí solo:

1. Cada reserva empieza bloqueando la fila del profesional (`SELECT … FOR UPDATE`). La segunda petición espera ahí hasta que la primera termina.
2. La tabla `appointments` tiene una restricción de exclusión que impide dos citas vigentes del mismo profesional con intervalos que se solapen:

   ~~~sql
   CONSTRAINT appointments_practitioner_no_overlap
       EXCLUDE USING gist (
           practitioner_id WITH =,
           tstzrange(starts_at, ends_at) WITH &&
       )
       WHERE (status = 'booked')
   ~~~

   Cuando la segunda petición por fin inserta, PostgreSQL encuentra la cita de la primera y rechaza el `INSERT` con el error `23P01`. El servicio lo traduce a `409`.

La garantía es la restricción, no el candado. Si una reserva llegara a la tabla por otro camino, sin tomar el candado, la base la rechazaría igual. El candado existe por I3 (sección 3) y, de paso, hace que la segunda petición falle rápido en lugar de esperar dentro del `INSERT`.

**Cómo se demuestra.** Con la prueba `I1 carrera` de `cmd/citascheck` (sección 7). En cada una de sus 20 rondas deja un solo espacio libre en un día, suelta 12 reservas a la vez por ese espacio y exige exactamente un `201`, once `409` por espacio tomado y una agenda sin espacios al final.

## 2. Nivel de aislamiento

Todas las transacciones de Agenda corren en **READ COMMITTED**, y el nivel se fija de forma explícita en cada una (`txOptions` en `appointments.go`), para que no dependa de la configuración por omisión del servidor ni del controlador.

### Por qué alcanza

El riesgo clásico de READ COMMITTED es decidir con una lectura que deja de ser cierta antes de escribir. Aquí ninguna decisión que sostiene un invariante depende de una lectura sin protección:

| Invariante | Qué lo garantiza | ¿Depende de una lectura previa? |
|---|---|---|
| I1, profesional sin solapes | Restricción de exclusión, comprobada al escribir | No |
| I2, paciente sin solapes | Restricción de exclusión equivalente sobre `patient_id` | No |
| I3, dentro del horario y fuera de bloqueos | Validación dentro de la transacción | Sí, y por eso se toma el candado del profesional |
| I4, duración fijada por el servicio | Trigger que calcula `ends_at` al insertar y otro que impide cambiarlo | No |
| I7, cancelar o reprogramar libera | El cambio de estado saca la cita de las restricciones en la misma transacción | No |

Las restricciones de exclusión se comprueban contra lo que hay en la tabla en el momento de escribir, incluidas las filas de transacciones que todavía no terminan: si hay una en curso que chocaría, el `INSERT` espera a ver si hace commit. Por eso funcionan igual en cualquier nivel de aislamiento.

### Por qué no SERIALIZABLE

SERIALIZABLE también sería correcto, pero resuelve el conflicto de otra forma: aborta una de las transacciones con el error `40001` y obliga a repetirla. Con dos recepcionistas compitiendo, la que pierde tendría que reintentar para descubrir, en el segundo intento, que el espacio ya no está. Con la restricción de exclusión la respuesta es un `409` directo y con un motivo preciso.

Además, SERIALIZABLE aborta transacciones que no chocan en realidad (falsos positivos del seguimiento de predicados), lo que bajo carga se traduce en reintentos sin motivo. Y seguiría haciendo falta la restricción, porque es lo único que protege la tabla de un camino de escritura que no pase por el servicio.

REPEATABLE READ no aporta nada aquí: la reserva necesita ver el estado más reciente al validar, no una foto del inicio de la transacción.

**Lo que se sacrifica** con READ COMMITTED es que la corrección depende de que el esquema y los candados estén bien puestos, no del motor en general. Una regla nueva que dependa de una lectura tendría que decidir explícitamente cómo se protege.

## 3. Candados

### El candado del profesional (I3)

Que una cita caiga dentro del horario y fuera de los bloqueos no se puede expresar como una restricción: depende de otras dos tablas y de la zona horaria de la clínica. Se valida leyendo, y esa lectura sí hay que protegerla. Si no, podría pasar esto:

1. La reserva lee los bloqueos del profesional: no hay ninguno a las 10:00.
2. Otra petición crea un bloqueo de 09:00 a 11:00 y hace commit.
3. La reserva inserta la cita de las 10:00, dentro de un bloqueo.

Para evitarlo, las cuatro operaciones que leen o cambian la agenda de un profesional empiezan bloqueando su fila en `practitioners`:

- reservar (RF9),
- reprogramar (RF10),
- cambiar el horario (RF5),
- crear o borrar un bloqueo (RF6).

Así ocurren una después de la otra para un mismo profesional, y lo que la reserva valida sigue siendo cierto cuando inserta. Profesionales distintos no se estorban.

Un bloqueo creado **después** de una reserva no viola I3 en el sentido del enunciado: RF6 dice que el bloqueo no cancela las citas ya reservadas, sino que las lista como conflictos. Lo mismo se hizo con los cambios de horario: `PUT …/schedule` devuelve las citas vigentes que quedaron fuera del horario nuevo.

### Deadlocks

Casi todas las operaciones toman un solo candado de profesional, así que no pueden formar un ciclo. La excepción es reprogramar una cita **hacia otro profesional**, que necesita los dos. Dos reprogramaciones cruzadas (una cita de A hacia B y otra de B hacia A) podrían bloquearse mutuamente.

Se evita tomando siempre los dos candados **en orden de id**, sin importar cuál es el origen y cuál el destino: las dos transacciones piden primero el mismo y la segunda espera sin retener nada que la primera necesite.

Queda una segunda línea de defensa: si PostgreSQL detectara un deadlock de todas formas (`40P01`), `TxManager` repite la transacción completa, hasta tres intentos con una espera creciente. Lo mismo hace con `40001`. Por eso las funciones que corren dentro de una transacción no tienen efectos fuera de la base hasta después del commit.

La cancelación y el desenlace solo actualizan la fila de la cita, con la condición `status = 'booked'` en el propio `UPDATE`. Si dos peticiones intentan cerrar la misma cita, la segunda espera a la primera y después no encuentra fila: responde `409`.

### Carreras que no pasan por el mismo candado (I2)

Dos reservas del **mismo paciente** a la misma hora con **profesionales distintos** bloquean filas diferentes, así que corren en paralelo. Aquí decide solo la restricción de exclusión del paciente: el segundo `INSERT` espera al primero y falla con `409` y el mensaje `the patient already has an appointment at that time`. La prueba `I2 carrera` lo ejercita 20 veces.

## 4. El ciclo de vida de la cita

**Estados (RF13).** Una cita nace en `booked` y solo desde ahí puede pasar a `attended`, `no_show`, `cancelled` o `rescheduled`, que son finales. La regla está en tres lugares: la función `canTransition`, la condición `status = 'booked'` de cada `UPDATE` y un trigger que rechaza cualquier cambio sobre una cita en estado final, por si algo llegara a la tabla sin pasar por el servicio.

**Reprogramar (RF10).** No mueve la cita: la cierra como `rescheduled` y crea una nueva que apunta a la anterior en `rescheduled_from_id`. Las dos sentencias van en una transacción y en ese orden:

1. `UPDATE` de la cita anterior a `rescheduled`. Deja de contar para las restricciones de exclusión.
2. `INSERT` de la cita nueva.

El orden importa porque las restricciones se comprueban en cada sentencia: liberar primero permite mover la cita a un espacio que se solapa con el que tenía (por ejemplo, diez minutos más tarde). Si el espacio nuevo no se puede tomar, la transacción se revierte y la cita original queda vigente, como si nada hubiera pasado. En ningún momento otra transacción puede ver las dos citas vigentes ni los dos espacios ocupados.

**Duración (I4).** La petición de reserva no lleva hora de fin ni duración. `ends_at` lo calcula un trigger a partir de la duración del servicio, e ignora cualquier valor que se envíe. Otro trigger impide modificarlo. Cambiar después la duración de un servicio no altera las citas ya reservadas.

**Desenlace (RF12).** Solo se admite si la cita ya empezó. La comparación usa el reloj de la base, dentro del mismo `UPDATE`, y además hay un `CHECK` en la tabla (`closed_at >= starts_at`).

## 5. Cálculo de la disponibilidad (RF7)

Para un profesional, una fecha y un servicio:

1. Se toman los bloques del horario semanal que corresponden al día de la semana de esa fecha y se convierten en intervalos concretos, **en la zona horaria de la clínica**. Los bloques contiguos se unen.
2. Se les restan los bloqueos y las citas vigentes.
3. Cada intervalo libre se divide en espacios consecutivos de la duración del servicio, empezando donde empieza el intervalo.

El paso 3 hace que después de una cita de 20 minutos los espacios de un servicio de 45 sigan desde ese minuto, sin dejar tiempo muerto. A cambio, los espacios ofrecidos no caen siempre en una cuadrícula fija. La reserva no exige esa cuadrícula: acepta cualquier instante que cumpla I1, I2 e I3.

El cálculo es una función pura (`availableSlots`), probada sin base de datos.

### Índice de RF7

La consulta que más se repite es "citas vigentes de este profesional que tocan este intervalo":

~~~sql
SELECT …
FROM appointments
WHERE clinic_id = $1
  AND practitioner_id = $2
  AND status = 'booked'
  AND tstzrange(starts_at, ends_at) && tstzrange($3, $4)
ORDER BY starts_at, id
~~~

Está escrita con la misma forma que la restricción de exclusión de I1, así que la sirve **el índice GiST parcial que crea esa restricción**. No hizo falta un índice adicional: el que garantiza I1 es el mismo que acelera RF7.

Plan de ejecución con 526 092 citas en la tabla (20 años de citas, una por hora, para tres profesionales):

~~~
Sort (actual rows=9)
  Sort Key: starts_at, id
  ->  Bitmap Heap Scan on appointments (actual rows=9)
        Recheck Cond: ((practitioner_id = '…') AND (tstzrange(starts_at, ends_at) && '[…)') AND (status = 'booked'))
        Filter: (clinic_id = '…')
        Heap Blocks: exact=1
        ->  Bitmap Index Scan on appointments_practitioner_no_overlap (actual rows=9)
              Index Cond: ((practitioner_id = '…') AND (tstzrange(starts_at, ends_at) && '[…)'))
Execution Time: 0.079 ms
~~~

Comparación sobre los mismos datos:

| Forma de resolver la consulta | Filas leídas | Tiempo |
|---|---|---|
| Índice GiST parcial de la restricción (el plan de arriba) | 9 | 0,08 ms |
| La misma consulta escrita como `starts_at < fin AND ends_at > inicio`, con el índice B-tree `(clinic_id, practitioner_id, starts_at)` | 100 289 | 17,3 ms |
| Recorrido secuencial (índices desactivados) | 526 092 | 39,4 ms |

La segunda fila explica por qué la consulta usa `tstzrange … &&` y no dos comparaciones: un B-tree solo puede acotar uno de los dos extremos, así que lee todas las citas anteriores al intervalo y descarta casi todas.

Los bloqueos se consultan igual, con el índice GiST `idx_blocks_practitioner_period`.

## 6. Caché de disponibilidad (I8)

Redis guarda respuestas de RF7 ya calculadas. PostgreSQL sigue siendo la única fuente de verdad: Redis no guarda nada que no se pueda recalcular, y por eso no tiene volumen en el Compose.

### Clave y alcance

~~~
avail:{clínica}:{profesional}:{fecha}:{servicio}:v{versión}
avail-version:{clínica}:{profesional}
~~~

Cada entrada es la lista de espacios de **un profesional, en una fecha, para un servicio**. Es el mismo grano de la consulta de RF7, de modo que un acierto responde la petición completa. RF8 (por servicio y rango de fechas) no tiene entradas propias: arma su respuesta con las de RF7, una por profesional y día.

La clínica forma parte de la clave. Un acierto no consulta la base, ni siquiera para comprobar que el profesional existe, y eso es seguro porque una entrada solo se guarda después de validar al profesional y al servicio en la clínica del token: un usuario de otra clínica construye otra clave, que nunca existió.

### Política de invalidación

Cada agenda (un profesional de una clínica) tiene un **número de versión** en Redis, y la versión es parte de la clave de sus entradas. **Invalidar es incrementar la versión** (`INCR`): todas las entradas anteriores, de cualquier fecha y servicio, quedan bajo claves que nadie vuelve a pedir.

Se eligió sobre las alternativas por estas razones:

| Alternativa | Problema |
|---|---|
| Solo expiración por tiempo | Admite una ventana en la que la caché contradice a la base. Es justo lo que I8 prohíbe |
| Borrar las claves afectadas (`DEL`) | Hay que saber cuáles son: una reserva afecta a todas las combinaciones de fecha y servicio de esa agenda. Además, tiene la carrera que se describe abajo |
| Escribir el valor nuevo en la caché al reservar | Obliga a recalcular en la ruta crítica y tiene la misma carrera entre dos escritores |

**La carrera que la versión evita.** Con borrado simple puede pasar esto:

1. Una consulta no encuentra la entrada y lee la base: el espacio de las 10:00 está libre.
2. Una reserva toma las 10:00, hace commit y borra la entrada (que todavía no existe).
3. La consulta guarda en la caché lo que leyó en el paso 1: las 10:00 libres. La caché queda mintiendo hasta que la entrada venza.

Con versiones, la consulta lee la versión **antes** de consultar la base y guarda su resultado bajo esa versión. En el paso 2 la versión sube, así que lo que se guarda en el paso 3 queda bajo una clave vieja que ya nadie pide.

**Cuándo se invalida.** En toda operación que cambia una agenda: reservar, reprogramar (las agendas de los dos profesionales si cambia), cancelar, registrar el desenlace, cambiar el horario y crear o borrar un bloqueo. También cuando el catálogo cambia algo de lo que depende la disponibilidad: los servicios que atiende un profesional, el retiro de un servicio y la activación o desactivación de un profesional.

**Dos incrementos por cambio.** Las operaciones de la agenda suben la versión dos veces: una dentro de la transacción, justo antes del commit, y otra después del commit.

- La primera hace que, desde ese instante, ninguna consulta acierte con una entrada anterior al cambio.
- Lo que se calcule entre las dos todavía pudo leer la base sin el cambio, porque no había hecho commit. La segunda lo deja huérfano.

Con un solo incremento después del commit quedaría una ventana de milisegundos entre el commit y el `INCR` en la que otro cliente podría acertar con una entrada vieja. Si la transacción se revierte después del primer incremento, la única consecuencia es un fallo de caché innecesario.

### Los espacios que ya pasaron

Una entrada guardada a las 9:00 incluye el espacio de las 9:30. A las 9:45 ese espacio ya no se debe ofrecer, pero la base no cambió y nada invalidó la entrada. Por eso los espacios que ya empezaron se quitan **después** de leer la caché, en cada respuesta: depende del reloj, no del estado de la base.

### Si Redis falla

| Falla | Qué hace Agenda |
|---|---|
| Redis no responde al leer | Calcula la respuesta contra la base y no guarda nada. La disponibilidad sigue funcionando, más lenta |
| Redis no responde al invalidar | Marca esa agenda como no confiable y deja de servirla desde la caché. En cada consulta posterior reintenta el `INCR`; cuando lo logra, la agenda vuelve a la caché ya con la versión nueva |
| Redis se reinicia y pierde todo | Se pierden las versiones y también las entradas, así que no queda nada viejo que servir |

El segundo caso es el que importa para I8: si la invalidación falla, en Redis sigue habiendo una entrada anterior a un cambio que ya hizo commit. Agenda prefiere no usar la caché para esa agenda antes que arriesgarse a servirla.

Las reservas nunca dependen de Redis: se validan siempre contra la base. Lo peor que puede producir la caché es ofrecer un espacio que, al intentar reservarlo, responde `409`.

Los timeouts de Redis son cortos (medio segundo para conectar, 300 ms por operación), para que un Redis lento no frene las consultas.

### Límites conocidos

- **La marca de "agenda no confiable" vive en memoria de cada proceso.** Con una sola instancia de Agenda, que es lo que levanta el Compose, es suficiente. Con varias instancias, si una no logra invalidar, las demás no se enteran. Para ese caso, y para el de un proceso que muere entre el commit y el segundo incremento, las entradas tienen una vida máxima de 10 minutos. Esa expiración no es la política de invalidación: es el límite del daño en un escenario de falla doble.
- **Los cambios del catálogo invalidan una sola vez**, después de hacer commit, porque son una sola sentencia. Queda la ventana de milisegundos descrita arriba para, por ejemplo, un profesional recién desactivado. Reservar con él se rechaza de todas formas.
- **RF8 hace dos lecturas a Redis por profesional y día.** Para rangos largos convendría agruparlas en una sola ida y vuelta (`MGET` o *pipeline*).

### Medición antes y después

Se midió la misma consulta con Agenda conectada a Redis y con Agenda arrancada sin `REDIS_ADDR`, que calcula siempre contra la base. La tabla tenía 526 092 citas.

| Consulta | Sin caché | Con caché | Mejora |
|---|---|---|---|
| RF7, un cliente: tiempo medio (p95) | 1,10 ms (1,66 ms) | 0,40 ms (0,63 ms) | 2,8 veces |
| RF7, 16 clientes a la vez: peticiones por segundo | 1 825 | 6 336 | 3,5 veces |
| RF8, 31 días, un cliente: tiempo medio (p95) | 14,16 ms (20,09 ms) | 3,02 ms (5,08 ms) | 4,7 veces |

Entorno de la medición: PostgreSQL 16 y Redis en la misma máquina que el servicio (2 CPU), sin Docker, con las peticiones directas a Agenda. **Los números absolutos cambiarán en el Compose**; lo que la medición muestra es la proporción.

Dos lecturas de estos números:

- Sin caché, cada consulta de RF7 hace siete lecturas a PostgreSQL (zona horaria, profesional, servicio, asociación, horario, bloqueos y citas). Con caché hace dos lecturas a Redis y ninguna a la base.
- La consulta sin caché ya es rápida, porque el índice de la sección 5 la resuelve en menos de una décima de milisegundo. La caché no rescata una consulta lenta: le quita a PostgreSQL la consulta más repetida, y eso se nota sobre todo con muchos clientes a la vez.

Para repetir la medición en el Compose:

~~~bash
# Con caché (el arranque normal)
docker compose up -d --build
docker compose --profile test run --build --rm availability-benchmark

# Sin caché: Agenda arranca sin REDIS_ADDR
REDIS_ADDR= docker compose up -d agenda
docker compose --profile test run --rm availability-benchmark

# Volver al arranque normal
docker compose up -d agenda
~~~

En PowerShell, la variable se fija con `$env:REDIS_ADDR = ""` antes del comando y se quita con `Remove-Item Env:REDIS_ADDR`. El programa imprime si Agenda está respondiendo con caché o sin ella.

## 7. Pruebas

Todas corren con `docker compose --profile test run --build --rm citas-api-integration-test` (o con el script de verificación completa) y están en `cmd/citascheck`. Las unitarias están en `internal/agenda/*_test.go`.

| Qué se demuestra | Prueba de integración | Prueba unitaria |
|---|---|---|
| Pregunta rectora, I1 | `I1 carrera: muchos clientes por el último espacio…` (20 rondas, 12 clientes); `RF9 I1 un espacio tomado o solapado responde 409` | — |
| I2 | `I2 el mismo paciente no puede tener dos citas solapadas…`; `I2 carrera: el mismo paciente con dos profesionales a la vez…` | — |
| I3 | `I3 no se reserva fuera del horario de atención`; `RF6 I3 un bloqueo retira la disponibilidad y rechaza reservas` | `TestWithinSchedule`, `TestBookRejectsUnavailableSlots` |
| I4 | `I4 la duración la fija el servicio, no la petición` | — |
| I7 | `RF10 I7 reprogramar libera el espacio anterior…`; `RF11 I7 el espacio cancelado vuelve a estar disponible de inmediato` | — |
| I8 | `I8 la disponibilidad no ofrece un espacio recién reservado…`; `I8 un cambio en los servicios del profesional también invalida la caché` | `TestInvalidateOrphansPreviousEntries`, `TestStaleReadCannotOverwriteAnInvalidation`, `TestFailedInvalidationDisablesTheCacheForThatAgenda` |
| Atomicidad de RF10 | `RF10 si el espacio nuevo no está libre, la cita original queda intacta` | `TestRescheduleRejections` |
| Orden de candados | — | `TestRescheduleLocksPractitionersInIDOrder` |
| RF13 | `RF13 un estado final no admite ninguna transición` | `TestCanTransition` |

La prueba de carrera es reproducible porque no depende de la suerte para que las peticiones coincidan: todos los clientes esperan la misma señal antes de enviar, y el resultado exigido (un `201` y el resto `409`) debe cumplirse en las 20 rondas.

Además de lo que quedó automatizado, durante el desarrollo se verificó a mano:

- La batería completa con Redis apagado y con Agenda sin `REDIS_ADDR`: todo pasa, sin caché.
- 40 pares de reprogramaciones cruzadas entre dos profesionales, lanzadas a la vez: las 80 se concedieron, sin deadlocks en el log de PostgreSQL y sin citas vigentes solapadas. No está automatizada porque la semilla no tiene dos profesionales que atiendan el mismo servicio.
