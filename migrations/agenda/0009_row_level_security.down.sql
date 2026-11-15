-- Deshace la migración 0009: quita RLS y sus funciones

DROP FUNCTION IF EXISTS practitioner_clinic_of_user(uuid);

DROP POLICY IF EXISTS clinic_isolation ON appointments;
ALTER TABLE appointments DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON blocks;
ALTER TABLE blocks DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON schedules;
ALTER TABLE schedules DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON patients;
ALTER TABLE patients DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON practitioner_services;
ALTER TABLE practitioner_services DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON practitioners;
ALTER TABLE practitioners DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON services;
ALTER TABLE services DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON specialties;
ALTER TABLE specialties DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS clinic_isolation ON clinics;
ALTER TABLE clinics DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS current_clinic_id();
