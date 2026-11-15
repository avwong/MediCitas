-- Datos de ejemplo para expediente_db
--
-- Son notas clínicas (RF15) de citas ya atendidas en seed/agenda.sql
-- Cada nota la escribió el profesional que atendió la cita
-- practitioner_subject es el subject de su usuario en Keycloak
--
-- Sirven para probar el aislamiento entre clínicas (I5) en Expediente
-- La API todavía no puede crear notas porque necesita la ruta de Agenda
-- que verifica la cita, así que sin esta semilla no habría notas que leer
--
-- Los datos son ficticios
-- ON CONFLICT DO NOTHING permite ejecutar la semilla varias veces

INSERT INTO clinical_notes (id, clinic_id, appointment_id, patient_id, practitioner_subject, content)
VALUES
    -- Clínica Demo
    ('0a1a1a1a-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'a1a1a1a1-0000-4000-8000-000000000001', 'b1b1b1b1-0000-4000-8000-000000000001',
     '44444444-4444-4444-8444-444444444444',
     'Paciente con tos y congestión de tres días, sin fiebre. Se indica reposo e hidratación y control en una semana.'),
    ('0a1a1a1a-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'a1a1a1a1-0000-4000-8000-000000000003', 'b1b1b1b1-0000-4000-8000-000000000005',
     '77777777-7777-4777-8777-777777777777',
     'Control de crecimiento sin hallazgos. Peso y talla dentro del rango esperado para la edad.'),

    -- Clínica Norte
    ('0a2a2a2a-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'a2a2a2a2-0000-4000-8000-000000000001', 'b2b2b2b2-0000-4000-8000-000000000002',
     '99999999-9999-4999-8999-999999999999',
     'Control prenatal de rutina. Signos vitales estables. Se solicitan los exámenes del siguiente trimestre.'),
    ('0a2a2a2a-0000-4000-8000-000000000002', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'a2a2a2a2-0000-4000-8000-000000000002', 'b2b2b2b2-0000-4000-8000-000000000003',
     '20202020-2020-4202-8202-202020202020',
     'Consulta por dolor lumbar de una semana. Sin signos de alarma. Se indica analgesia y ejercicios.'),

    -- Clínica Sur
    ('0a3a3a3a-0000-4000-8000-000000000001', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'a3a3a3a3-0000-4000-8000-000000000001', 'b3b3b3b3-0000-4000-8000-000000000001',
     '50505050-5050-4505-8505-505050505050',
     'Consulta general por cefalea ocasional. Examen físico normal. Se recomienda registrar los episodios.')
ON CONFLICT DO NOTHING;
