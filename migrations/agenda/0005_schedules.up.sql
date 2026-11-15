-- Agenda: horario de atención semanal de cada profesional (RF5).
--
-- Cada fila es un bloque de atención en un día de la semana, así que una
-- jornada partida en mañana y tarde son dos filas. Las horas son horas de
-- pared en la zona horaria de la clínica (clinics.timezone): "08:00" es las
-- ocho de la mañana en la clínica, también cuando cambia el horario de verano.
--
-- El horario es el vigente: no guarda historia. Cambiarlo afecta la
-- disponibilidad hacia adelante y no toca las citas ya reservadas.

-- PostgreSQL no trae un tipo de rango para TIME. Hace falta para que la
-- restricción de exclusión de abajo compare bloques con el operador &&.
CREATE TYPE timerange AS RANGE (subtype = time);

CREATE TABLE schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL,
    practitioner_id UUID NOT NULL,

    -- Día de la semana según ISO 8601: 1 es lunes y 7 es domingo. Coincide
    -- con EXTRACT(ISODOW FROM ...).
    weekday SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7),
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Un bloque no cruza la medianoche: empieza y termina el mismo día.
    CONSTRAINT schedules_time_order CHECK (start_time < end_time),

    CONSTRAINT schedules_clinic_id_unique UNIQUE (clinic_id, id),

    CONSTRAINT schedules_practitioner_fk
        FOREIGN KEY (clinic_id, practitioner_id)
        REFERENCES practitioners (clinic_id, id)
        ON DELETE CASCADE,

    -- Dos bloques del mismo profesional no se solapan en un mismo día. El
    -- rango es [inicio, fin), así que 08:00-12:00 y 12:00-17:00 son válidos.
    -- Sin esta regla, la disponibilidad ofrecería dos veces el mismo espacio.
    CONSTRAINT schedules_no_overlap
        EXCLUDE USING gist (
            practitioner_id WITH =,
            weekday WITH =,
            timerange(start_time, end_time) WITH &&
        )
);

COMMENT ON TABLE schedules IS
    'Horario semanal vigente de cada profesional (RF5). Las horas están en la zona horaria de la clínica.';

-- La disponibilidad (RF7) pide "los bloques de este profesional en este día
-- de la semana". El índice de schedules_no_overlap empieza por
-- (practitioner_id, weekday) y cubre esa búsqueda; este otro cubre el listado
-- del horario completo de un profesional dentro de su clínica.
CREATE INDEX idx_schedules_practitioner
    ON schedules (clinic_id, practitioner_id, weekday, start_time);
