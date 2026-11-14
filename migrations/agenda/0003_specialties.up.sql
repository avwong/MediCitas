-- Agenda: normaliza las especialidades en su propia tabla
--
-- Antes, specialty era texto libre repetido en services y practitioners
-- El nombre de la especialidad se guardaba una vez por fila
-- Podía desincronizarse ("Medicina general" vs "medicina general")
-- Moverlo a su propia tabla elimina esa dependencia transitiva (3FN)
-- Ahora el nombre depende de la especialidad, no del servicio ni del profesional
--
-- El catálogo es por clínica
-- Lleva clinic_id, como el resto de las tablas
-- Se referencia con una llave foránea compuesta (clinic_id, id)
-- Así la base rechaza un servicio o profesional con una especialidad de otra clínica (I5)

CREATE TABLE specialties (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics (id) ON DELETE RESTRICT,
    name VARCHAR(120) NOT NULL CHECK (length(btrim(name)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT specialties_clinic_id_unique UNIQUE (clinic_id, id)
);

-- Una clínica no puede tener la misma especialidad dos veces
-- La comparación no distingue mayúsculas
CREATE UNIQUE INDEX specialties_clinic_name_unique
    ON specialties (clinic_id, lower(name));

-- Migración de datos existentes
-- Crea una especialidad por cada (clínica, nombre) distinto en uso
-- Unifica las grafías que solo difieren en mayúsculas
INSERT INTO specialties (clinic_id, name)
SELECT DISTINCT ON (clinic_id, lower(name)) clinic_id, name
FROM (
    SELECT clinic_id, btrim(specialty) AS name FROM services
    UNION ALL
    SELECT clinic_id, btrim(specialty) AS name FROM practitioners
) used
ORDER BY clinic_id, lower(name), name;

-- services
ALTER TABLE services ADD COLUMN specialty_id UUID;

UPDATE services s
SET specialty_id = sp.id
FROM specialties sp
WHERE sp.clinic_id = s.clinic_id
  AND lower(sp.name) = lower(btrim(s.specialty));

ALTER TABLE services
    ALTER COLUMN specialty_id SET NOT NULL,
    ADD CONSTRAINT services_specialty_fk
        FOREIGN KEY (clinic_id, specialty_id)
        REFERENCES specialties (clinic_id, id)
        ON DELETE RESTRICT,
    DROP COLUMN specialty;

-- practitioners
ALTER TABLE practitioners ADD COLUMN specialty_id UUID;

UPDATE practitioners p
SET specialty_id = sp.id
FROM specialties sp
WHERE sp.clinic_id = p.clinic_id
  AND lower(sp.name) = lower(btrim(p.specialty));

ALTER TABLE practitioners
    ALTER COLUMN specialty_id SET NOT NULL,
    ADD CONSTRAINT practitioners_specialty_fk
        FOREIGN KEY (clinic_id, specialty_id)
        REFERENCES specialties (clinic_id, id)
        ON DELETE RESTRICT,
    DROP COLUMN specialty;

-- Índices por especialidad
-- Sirven para buscar los servicios o profesionales de una especialidad
-- Sirven para comprobar que un servicio requiere la especialidad del profesional (RF4)
CREATE INDEX idx_services_specialty
    ON services (clinic_id, specialty_id);

CREATE INDEX idx_practitioners_specialty
    ON practitioners (clinic_id, specialty_id);
