-- Datos de ejemplo para agenda_db.
--
-- Son coherentes con seed/tenancy.sql: las clínicas y los profesionales
-- coinciden con sus usuarios. Hay tres clínicas con tres profesionales cada
-- una, y cada profesional atiende todos los servicios de su especialidad.
--
-- Todos los ids son fijos, para que las pruebas y otras semillas puedan
-- referenciarlos. ON CONFLICT DO NOTHING permite ejecutar la semilla varias
-- veces sin que falle.

INSERT INTO clinics (id, timezone)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'America/Costa_Rica'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'America/Costa_Rica'),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'America/Bogota')
ON CONFLICT (id) DO NOTHING;

-- Especialidades. El prefijo del id indica la clínica: e1e1 Demo, e2e2 Norte,
-- e3e3 Sur.
INSERT INTO specialties (id, clinic_id, name)
VALUES
    -- Clínica Demo
    ('e1e1e1e1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Medicina general'),
    ('e1e1e1e1-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Pediatría'),
    ('e1e1e1e1-0000-4000-8000-000000000003', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Cardiología'),

    -- Clínica Norte
    ('e2e2e2e2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Medicina general'),
    ('e2e2e2e2-0000-4000-8000-000000000002', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Ginecología'),
    ('e2e2e2e2-0000-4000-8000-000000000003', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Dermatología'),

    -- Clínica Sur
    ('e3e3e3e3-0000-4000-8000-000000000001', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Medicina general'),
    ('e3e3e3e3-0000-4000-8000-000000000002', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Pediatría'),
    ('e3e3e3e3-0000-4000-8000-000000000003', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Odontología')
ON CONFLICT (id) DO NOTHING;

-- Servicios. Las duraciones varían entre 20 y 45 minutos para probar la
-- disponibilidad. El prefijo del id indica la clínica: d1d1 Demo, d2d2 Norte,
-- d3d3 Sur.
INSERT INTO services (id, clinic_id, name, duration_minutes, specialty_id)
VALUES
    -- Clínica Demo
    ('d1d1d1d1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Consulta general', 30, 'e1e1e1e1-0000-4000-8000-000000000001'),
    ('d1d1d1d1-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Control de seguimiento', 20, 'e1e1e1e1-0000-4000-8000-000000000001'),
    ('d1d1d1d1-0000-4000-8000-000000000003', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Consulta pediátrica', 30, 'e1e1e1e1-0000-4000-8000-000000000002'),
    ('d1d1d1d1-0000-4000-8000-000000000004', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Control de crecimiento', 20, 'e1e1e1e1-0000-4000-8000-000000000002'),
    ('d1d1d1d1-0000-4000-8000-000000000005', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Consulta cardiológica', 45, 'e1e1e1e1-0000-4000-8000-000000000003'),
    ('d1d1d1d1-0000-4000-8000-000000000006', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Electrocardiograma', 30, 'e1e1e1e1-0000-4000-8000-000000000003'),

    -- Clínica Norte
    ('d2d2d2d2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Consulta general', 30, 'e2e2e2e2-0000-4000-8000-000000000001'),
    ('d2d2d2d2-0000-4000-8000-000000000002', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Consulta ginecológica', 30, 'e2e2e2e2-0000-4000-8000-000000000002'),
    ('d2d2d2d2-0000-4000-8000-000000000003', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Control prenatal', 40, 'e2e2e2e2-0000-4000-8000-000000000002'),
    ('d2d2d2d2-0000-4000-8000-000000000004', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Consulta dermatológica', 30, 'e2e2e2e2-0000-4000-8000-000000000003'),
    ('d2d2d2d2-0000-4000-8000-000000000005', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Tratamiento de lesiones', 20, 'e2e2e2e2-0000-4000-8000-000000000003'),

    -- Clínica Sur
    ('d3d3d3d3-0000-4000-8000-000000000001', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Consulta general', 30, 'e3e3e3e3-0000-4000-8000-000000000001'),
    ('d3d3d3d3-0000-4000-8000-000000000002', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Control de seguimiento', 20, 'e3e3e3e3-0000-4000-8000-000000000001'),
    ('d3d3d3d3-0000-4000-8000-000000000003', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Consulta pediátrica', 30, 'e3e3e3e3-0000-4000-8000-000000000002'),
    ('d3d3d3d3-0000-4000-8000-000000000004', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Consulta odontológica', 40, 'e3e3e3e3-0000-4000-8000-000000000003'),
    ('d3d3d3d3-0000-4000-8000-000000000005', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     'Limpieza dental', 30, 'e3e3e3e3-0000-4000-8000-000000000003')
ON CONFLICT (id) DO NOTHING;

-- Profesionales. user_id y user_subject apuntan al usuario correspondiente en
-- Tenancy y en Keycloak. El prefijo del id indica la clínica: c1c1 Demo, c2c2
-- Norte, c3c3 Sur.
INSERT INTO practitioners (id, clinic_id, user_id, user_subject, display_name, specialty_id)
VALUES
    -- Clínica Demo
    ('c1c1c1c1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '44444444-aaaa-4444-8444-444444444444', '44444444-4444-4444-8444-444444444444',
     'Dra. Ana Prueba', 'e1e1e1e1-0000-4000-8000-000000000001'),
    ('c1c1c1c1-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '77777777-aaaa-4777-8777-777777777777', '77777777-7777-4777-8777-777777777777',
     'Dr. Luis Mora', 'e1e1e1e1-0000-4000-8000-000000000002'),
    ('c1c1c1c1-0000-4000-8000-000000000003', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '88888888-aaaa-4888-8888-888888888888', '88888888-8888-4888-8888-888888888888',
     'Dra. Sofía Vargas', 'e1e1e1e1-0000-4000-8000-000000000003'),

    -- Clínica Norte
    ('c2c2c2c2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '99999999-aaaa-4999-8999-999999999999', '99999999-9999-4999-8999-999999999999',
     'Dra. Carmen Solís', 'e2e2e2e2-0000-4000-8000-000000000002'),
    ('c2c2c2c2-0000-4000-8000-000000000002', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '10101010-aaaa-4101-8101-101010101010', '10101010-1010-4101-8101-101010101010',
     'Dr. Rafael Araya', 'e2e2e2e2-0000-4000-8000-000000000003'),
    ('c2c2c2c2-0000-4000-8000-000000000003', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '20202020-aaaa-4202-8202-202020202020', '20202020-2020-4202-8202-202020202020',
     'Dra. Elena Brenes', 'e2e2e2e2-0000-4000-8000-000000000001'),

    -- Clínica Sur
    ('c3c3c3c3-0000-4000-8000-000000000001', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '50505050-aaaa-4505-8505-505050505050', '50505050-5050-4505-8505-505050505050',
     'Dra. Camila Ríos', 'e3e3e3e3-0000-4000-8000-000000000001'),
    ('c3c3c3c3-0000-4000-8000-000000000002', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '60606060-aaaa-4606-8606-606060606060', '60606060-6060-4606-8606-606060606060',
     'Dr. Mateo Herrera', 'e3e3e3e3-0000-4000-8000-000000000002'),
    ('c3c3c3c3-0000-4000-8000-000000000003', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '70707070-aaaa-4707-8707-707070707070', '70707070-7070-4707-8707-707070707070',
     'Dra. Valentina Cruz', 'e3e3e3e3-0000-4000-8000-000000000003')
ON CONFLICT (id) DO NOTHING;

-- Servicios que atiende cada profesional: todos los de su especialidad.
INSERT INTO practitioner_services (clinic_id, practitioner_id, service_id)
VALUES
    -- Clínica Demo
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000001',
     'd1d1d1d1-0000-4000-8000-000000000001'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000001',
     'd1d1d1d1-0000-4000-8000-000000000002'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000002',
     'd1d1d1d1-0000-4000-8000-000000000003'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000002',
     'd1d1d1d1-0000-4000-8000-000000000004'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000003',
     'd1d1d1d1-0000-4000-8000-000000000005'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000003',
     'd1d1d1d1-0000-4000-8000-000000000006'),

    -- Clínica Norte
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'c2c2c2c2-0000-4000-8000-000000000001',
     'd2d2d2d2-0000-4000-8000-000000000002'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'c2c2c2c2-0000-4000-8000-000000000001',
     'd2d2d2d2-0000-4000-8000-000000000003'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'c2c2c2c2-0000-4000-8000-000000000002',
     'd2d2d2d2-0000-4000-8000-000000000004'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'c2c2c2c2-0000-4000-8000-000000000002',
     'd2d2d2d2-0000-4000-8000-000000000005'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'c2c2c2c2-0000-4000-8000-000000000003',
     'd2d2d2d2-0000-4000-8000-000000000001'),

    -- Clínica Sur
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'c3c3c3c3-0000-4000-8000-000000000001',
     'd3d3d3d3-0000-4000-8000-000000000001'),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'c3c3c3c3-0000-4000-8000-000000000001',
     'd3d3d3d3-0000-4000-8000-000000000002'),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'c3c3c3c3-0000-4000-8000-000000000002',
     'd3d3d3d3-0000-4000-8000-000000000003'),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'c3c3c3c3-0000-4000-8000-000000000003',
     'd3d3d3d3-0000-4000-8000-000000000004'),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'c3c3c3c3-0000-4000-8000-000000000003',
     'd3d3d3d3-0000-4000-8000-000000000005')
ON CONFLICT DO NOTHING;

-- Pacientes de ejemplo (RF14). Son datos ficticios: las cédulas, los teléfonos
-- y los correos no corresponden a personas reales, y los correos usan
-- example.com, un dominio reservado para ejemplos. Varios pacientes omiten
-- algún dato de contacto, porque el contacto es opcional.
INSERT INTO patients (id, clinic_id, identification, first_name, last_name, birth_date, phone, email, address)
VALUES
    -- Clínica Demo
    ('b1b1b1b1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '1-0801-0123', 'María Fernanda', 'Rojas Vargas', '1988-03-14',
     '+506 8801-2345', 'maria.rojas@example.com', 'San José, Barrio Escalante'),
    ('b1b1b1b1-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '3-0456-0789', 'Carlos Andrés', 'Mora Solano', '1975-11-02',
     '2551-6677', 'carlos.mora@example.com', 'Cartago, Centro'),
    ('b1b1b1b1-0000-4000-8000-000000000003', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '1-1234-0567', 'Lucía', 'Jiménez Araya', '2001-07-21',
     '+506 7012-3344', NULL, 'Heredia, San Francisco'),
    ('b1b1b1b1-0000-4000-8000-000000000004', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '2-0678-0912', 'Jorge Luis', 'Castro Núñez', '1962-01-30',
     '2222-0011', 'jorge.castro@example.com', NULL),
    ('b1b1b1b1-0000-4000-8000-000000000005', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '1-1456-0321', 'Valeria', 'Quesada Brenes', '2015-09-09',
     '+506 8890-1122', NULL, 'Alajuela, Centro'),
    ('b1b1b1b1-0000-4000-8000-000000000006', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '4-0210-0654', 'Daniel Esteban', 'Vega Mora', '1995-12-25',
     NULL, NULL, NULL),

    -- Clínica Norte. La identificación 1-0801-0123 también existe en Demo, y
    -- es válido: la identificación solo es única dentro de cada clínica.
    ('b2b2b2b2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '1-0801-0123', 'María Fernanda', 'Rojas Vargas', '1988-03-14',
     '+506 8801-2345', 'maria.rojas@example.com', 'San José, Barrio Escalante'),
    ('b2b2b2b2-0000-4000-8000-000000000002', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '5-0333-0444', 'Andrea', 'Salas Campos', '1990-04-18',
     '+506 6055-7788', 'andrea.salas@example.com', NULL),
    ('b2b2b2b2-0000-4000-8000-000000000003', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '6-0150-0987', 'Pedro Pablo', 'Chacón Ureña', '1950-06-06',
     '2663-4455', NULL, 'Liberia, Guanacaste'),

    -- Clínica Sur
    ('b3b3b3b3-0000-4000-8000-000000000001', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '1032456789', 'Sebastián', 'Gómez Peña', '1992-08-03',
     '+57 310 555 0142', 'sebastian.gomez@example.com', 'Bogotá, Chapinero'),
    ('b3b3b3b3-0000-4000-8000-000000000002', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '52987654', 'Marcela', 'Ortiz Duarte', '1979-02-17',
     '+57 320 555 0188', NULL, 'Bogotá, Usaquén'),
    ('b3b3b3b3-0000-4000-8000-000000000003', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '1098765432', 'Tomás', 'Rincón Silva', '2010-11-30',
     '+57 315 555 0170', 'acudiente.rincon@example.com', NULL),
    ('b3b3b3b3-0000-4000-8000-000000000004', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
     '79456123', 'Isabel', 'Cárdenas Mejía', '1965-05-09',
     NULL, NULL, NULL)
ON CONFLICT DO NOTHING;
