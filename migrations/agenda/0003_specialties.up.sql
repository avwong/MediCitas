-- Agenda: normaliza las especialidades en su propia tabla.
--
-- Antes de esta migración "specialty" era texto libre repetido en services y
-- practitioners, así que un mismo hecho (el nombre de una especialidad) se
-- guardaba una vez por fila y podía desincronizarse ("Medicina general" vs
-- "medicina general"). Moverlo a su propia tabla elimina esa dependencia
-- transitiva (3FN): el nombre depende de la especialidad, no del servicio ni
-- del profesional.
--
-- El catálogo es por clínica. Como el resto de las tablas, lleva clinic_id y
-- se referencia mediante una llave foránea compuesta (clinic_id, id), de modo
-- que la base de datos rechaza un servicio o profesional que apunte a una
-- especialidad de otra clínica (I5).

CREATE TABLE specialties (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    clinic_id UUID NOT NULL REFERENCES clinics (id) ON DELETE RESTRICT,
    name VARCHAR(120) NOT NULL CHECK (length(btrim(name)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT specialties_clinic_id_unique UNIQUE (clinic_id, id)
);

-- Una clínica no puede tener la misma especialidad dos veces, sin importar
-- las mayúsculas.
CREATE UNIQUE INDEX specialties_clinic_name_unique
    ON specialties (clinic_id, lower(name));

-- Migración de datos: una especialidad por cada (clínica, nombre) distinto en
-- uso, unificando las grafías que solo difieren en mayúsculas.
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

-- Sirven para "servicios / profesionales de una especialidad" y para
-- comprobar que un servicio requiere la especialidad del profesional (RF4).
CREATE INDEX idx_services_specialty
    ON services (clinic_id, specialty_id);

CREATE INDEX idx_practitioners_specialty
    ON practitioners (clinic_id, specialty_id);
