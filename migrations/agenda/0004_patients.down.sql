-- Elimina la tabla de pacientes. Si ya existen citas que la referencian, esas
-- migraciones deben revertirse antes.

DROP INDEX IF EXISTS idx_patients_clinic_name;

DROP TABLE IF EXISTS patients;
