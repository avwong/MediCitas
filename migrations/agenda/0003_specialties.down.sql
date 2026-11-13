-- Restaura las columnas de texto libre "specialty" a partir de la tabla
-- specialties.

ALTER TABLE services ADD COLUMN specialty VARCHAR(120);

UPDATE services s
SET specialty = sp.name
FROM specialties sp
WHERE sp.clinic_id = s.clinic_id
  AND sp.id = s.specialty_id;

ALTER TABLE services
    ALTER COLUMN specialty SET NOT NULL;

DROP INDEX IF EXISTS idx_services_specialty;

ALTER TABLE services
    DROP CONSTRAINT services_specialty_fk,
    DROP COLUMN specialty_id;

ALTER TABLE practitioners ADD COLUMN specialty VARCHAR(120);

UPDATE practitioners p
SET specialty = sp.name
FROM specialties sp
WHERE sp.clinic_id = p.clinic_id
  AND sp.id = p.specialty_id;

ALTER TABLE practitioners
    ALTER COLUMN specialty SET NOT NULL;

DROP INDEX IF EXISTS idx_practitioners_specialty;

ALTER TABLE practitioners
    DROP CONSTRAINT practitioners_specialty_fk,
    DROP COLUMN specialty_id;

DROP TABLE IF EXISTS specialties;
