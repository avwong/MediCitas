-- Agenda: citas y su ciclo de vida (RF9 a RF13).
--
-- Este esquema es la estrategia de concurrencia de la reserva. Lo que debe
-- ser cierto aunque dos peticiones lleguen a la vez lo garantiza la base, no
-- una lectura previa de la aplicación:
--
--   I1 e I2  restricciones de exclusión sobre las citas reservadas
--   I4       ends_at lo calcula un trigger a partir de la duración del servicio
--   I7       cancelar o reprogramar saca la cita de esas restricciones en la
--            misma transacción
--   RF13     un trigger rechaza cualquier salida de un estado final
--
-- I3 (dentro del horario y fuera de bloqueos) no se puede expresar como una
-- restricción: lo valida la aplicación dentro de la transacción de reserva,
-- con la fila del profesional bloqueada (SELECT ... FOR UPDATE).

CREATE TABLE appointments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL,
    patient_id UUID NOT NULL,
    practitioner_id UUID NOT NULL,
    service_id UUID NOT NULL,

    starts_at TIMESTAMPTZ NOT NULL,
    -- Lo fija el trigger appointments_set_ends_at; el valor que envíe la
    -- aplicación se ignora (I4).
    ends_at TIMESTAMPTZ NOT NULL,

    -- booked es el único estado vigente. Los otros cuatro son finales.
    status VARCHAR(16) NOT NULL DEFAULT 'booked' CHECK (
        status IN ('booked', 'attended', 'no_show', 'cancelled', 'rescheduled')
    ),

    -- Reprogramar (RF10) no mueve la cita: la deja en rescheduled y crea una
    -- nueva, que apunta aquí a la anterior. Así el espacio viejo se libera y
    -- el nuevo se ocupa en una sola transacción, y queda el rastro. El orden
    -- importa: primero el UPDATE de la anterior y después el INSERT de la
    -- nueva, porque las restricciones de exclusión se comprueban en cada
    -- sentencia.
    rescheduled_from_id UUID,

    -- Subject de Keycloak de quien reservó.
    created_by VARCHAR(64) NOT NULL CHECK (length(btrim(created_by)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Una cita sale de booked una sola vez, así que basta un par de columnas
    -- para saber quién la cerró y cuándo, sea cual sea el estado final.
    closed_by VARCHAR(64),
    closed_at TIMESTAMPTZ,
    -- Motivo de la cancelación (RF11).
    cancellation_reason VARCHAR(300),

    CONSTRAINT appointments_time_order CHECK (starts_at < ends_at),

    -- Los IS NOT NULL explícitos hacen falta: un CHECK cuyo resultado es NULL
    -- se da por cumplido, y length(NULL) es NULL.
    CONSTRAINT appointments_closed_consistent CHECK (
        (status = 'booked' AND closed_by IS NULL AND closed_at IS NULL)
        OR
        (status <> 'booked'
            AND closed_at IS NOT NULL
            AND closed_by IS NOT NULL
            AND length(btrim(closed_by)) > 0)
    ),

    -- RF12: el desenlace solo se registra una vez pasada la hora de inicio.
    -- La aplicación lo valida antes; esta regla lo sostiene en la base.
    CONSTRAINT appointments_outcome_after_start CHECK (
        status NOT IN ('attended', 'no_show') OR closed_at >= starts_at
    ),

    CONSTRAINT appointments_cancellation_reason_consistent CHECK (
        (status = 'cancelled'
            AND cancellation_reason IS NOT NULL
            AND length(btrim(cancellation_reason)) > 0)
        OR
        (status <> 'cancelled' AND cancellation_reason IS NULL)
    ),

    CONSTRAINT appointments_clinic_id_unique UNIQUE (clinic_id, id),

    -- Una cita se reprograma a lo sumo una vez, porque rescheduled es final.
    CONSTRAINT appointments_rescheduled_from_unique UNIQUE (rescheduled_from_id),

    CONSTRAINT appointments_patient_fk
        FOREIGN KEY (clinic_id, patient_id)
        REFERENCES patients (clinic_id, id)
        ON DELETE RESTRICT,

    CONSTRAINT appointments_practitioner_fk
        FOREIGN KEY (clinic_id, practitioner_id)
        REFERENCES practitioners (clinic_id, id)
        ON DELETE RESTRICT,

    -- No se referencia practitioner_services: esa asociación se puede quitar
    -- después, y la cita debe seguir existiendo. Que el profesional atienda
    -- el servicio lo valida la reserva, con la fila del profesional bloqueada
    -- (el mismo bloqueo que toma el cambio de sus servicios).
    CONSTRAINT appointments_service_fk
        FOREIGN KEY (clinic_id, service_id)
        REFERENCES services (clinic_id, id)
        ON DELETE RESTRICT,

    CONSTRAINT appointments_rescheduled_from_fk
        FOREIGN KEY (clinic_id, rescheduled_from_id)
        REFERENCES appointments (clinic_id, id)
        ON DELETE RESTRICT,

    -- I1: un profesional no tiene dos citas reservadas que se solapen.
    --
    -- El rango es [inicio, fin), así que una cita puede empezar justo cuando
    -- termina la anterior. Si dos reservas compiten por el mismo espacio, la
    -- segunda espera a que la primera termine su transacción y entonces
    -- recibe el error 23P01 (exclusion_violation), que la API responde como
    -- 409 "espacio ya tomado". No depende del nivel de aislamiento.
    CONSTRAINT appointments_practitioner_no_overlap
        EXCLUDE USING gist (
            practitioner_id WITH =,
            tstzrange(starts_at, ends_at) WITH &&
        )
        WHERE (status = 'booked'),

    -- I2: un paciente no tiene dos citas reservadas solapadas en su clínica.
    CONSTRAINT appointments_patient_no_overlap
        EXCLUDE USING gist (
            patient_id WITH =,
            tstzrange(starts_at, ends_at) WITH &&
        )
        WHERE (status = 'booked')
);

COMMENT ON TABLE appointments IS
    'Citas (RF9 a RF13). Solo status = booked ocupa espacio en la agenda; los demás estados son finales.';

COMMENT ON COLUMN appointments.ends_at IS
    'starts_at más la duración del servicio en el momento de reservar. Lo calcula un trigger y no se puede modificar (I4).';

-- I4: la duración de la cita la determina el servicio.
--
-- ends_at es una copia calculada: se guarda porque las restricciones de
-- exclusión necesitan el intervalo en la propia fila. Queda fijada al
-- reservar; si después cambiara la duración del servicio, las citas ya
-- reservadas conservan la suya.
CREATE FUNCTION set_appointment_ends_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    service_minutes INTEGER;
BEGIN
    SELECT duration_minutes
    INTO service_minutes
    FROM services
    WHERE clinic_id = NEW.clinic_id
      AND id = NEW.service_id;

    -- Si el servicio no existe en la clínica, se deja pasar: la llave
    -- foránea rechaza la fila con su propio error (23503).
    IF service_minutes IS NOT NULL THEN
        NEW.ends_at := NEW.starts_at + make_interval(mins => service_minutes);
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER appointments_set_ends_at
BEFORE INSERT ON appointments
FOR EACH ROW
EXECUTE FUNCTION set_appointment_ends_at();

-- RF13 e I4: después de creada, de una cita solo cambia su cierre.
--
-- La aplicación ya comprueba la transición antes de escribir; este trigger es
-- la garantía de que ninguna ruta, presente o futura, pueda saltársela.
CREATE FUNCTION guard_appointment_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status <> 'booked' THEN
        RAISE EXCEPTION 'appointment % is % and cannot change', OLD.id, OLD.status
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.status = 'booked' THEN
        RAISE EXCEPTION 'a booked appointment can only move to a final status'
            USING ERRCODE = 'check_violation';
    END IF;

    IF (NEW.clinic_id, NEW.patient_id, NEW.practitioner_id, NEW.service_id,
        NEW.starts_at, NEW.ends_at, NEW.created_by, NEW.created_at)
       IS DISTINCT FROM
       (OLD.clinic_id, OLD.patient_id, OLD.practitioner_id, OLD.service_id,
        OLD.starts_at, OLD.ends_at, OLD.created_by, OLD.created_at)
       OR NEW.rescheduled_from_id IS DISTINCT FROM OLD.rescheduled_from_id
    THEN
        RAISE EXCEPTION 'only the status and closing data of an appointment can change'
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER appointments_guard_update
BEFORE UPDATE ON appointments
FOR EACH ROW
EXECUTE FUNCTION guard_appointment_update();

-- Los índices de las dos restricciones de exclusión ya cubren la consulta
-- más frecuente, la disponibilidad (RF7): "citas reservadas de este
-- profesional que se solapan con este día". Los de abajo cubren los listados.

-- Agenda del día de un profesional (RF20).
CREATE INDEX idx_appointments_practitioner_day
    ON appointments (clinic_id, practitioner_id, starts_at);

-- Agenda del día de toda la clínica (RF20) y conteo por estado en un rango de
-- fechas (RF21).
CREATE INDEX idx_appointments_clinic_day
    ON appointments (clinic_id, starts_at);

-- Historial de citas de un paciente (RF16).
CREATE INDEX idx_appointments_patient
    ON appointments (clinic_id, patient_id, starts_at DESC);
