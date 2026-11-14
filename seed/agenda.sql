-- Demo data for agenda_db, consistent with seed/tenancy.sql.

INSERT INTO clinics (id, timezone)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'America/Costa_Rica'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'America/Costa_Rica')
ON CONFLICT (id) DO NOTHING;

INSERT INTO specialties (id, clinic_id, name)
VALUES
    ('e1e1e1e1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Medicina general'),
    ('e2e2e2e2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Medicina general')
ON CONFLICT (id) DO NOTHING;

INSERT INTO services (id, clinic_id, name, duration_minutes, specialty_id)
VALUES
    ('d1d1d1d1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Consulta general', 30, 'e1e1e1e1-0000-4000-8000-000000000001'),
    ('d1d1d1d1-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Control de seguimiento', 20, 'e1e1e1e1-0000-4000-8000-000000000001'),
    ('d2d2d2d2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Consulta general', 30, 'e2e2e2e2-0000-4000-8000-000000000001')
ON CONFLICT (id) DO NOTHING;

INSERT INTO practitioners (id, clinic_id, user_id, user_subject, display_name, specialty_id)
VALUES
    ('c1c1c1c1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '44444444-aaaa-4444-8444-444444444444', '44444444-4444-4444-8444-444444444444',
     'Dra. Ana Prueba', 'e1e1e1e1-0000-4000-8000-000000000001')
ON CONFLICT (id) DO NOTHING;

INSERT INTO practitioner_services (clinic_id, practitioner_id, service_id)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000001',
     'd1d1d1d1-0000-4000-8000-000000000001'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000001',
     'd1d1d1d1-0000-4000-8000-000000000002')
ON CONFLICT DO NOTHING;

-- Pacientes de ejemplo (RF14)
-- Datos ficticios: las cédulas, teléfonos y correos no corresponden a personas reales
-- Los correos usan example.com, un dominio reservado para ejemplos
-- Varios pacientes omiten algún dato de contacto, porque el contacto es opcional
-- ON CONFLICT DO NOTHING sin objetivo cubre el id y la identificación repetida
-- Así la semilla se puede ejecutar varias veces sin fallar
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

    -- Clínica Norte
    -- La identificación 1-0801-0123 también existe en Demo
    -- Es válido: la identificación solo es única dentro de cada clínica
    ('b2b2b2b2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '1-0801-0123', 'María Fernanda', 'Rojas Vargas', '1988-03-14',
     '+506 8801-2345', 'maria.rojas@example.com', 'San José, Barrio Escalante'),
    ('b2b2b2b2-0000-4000-8000-000000000002', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '5-0333-0444', 'Andrea', 'Salas Campos', '1990-04-18',
     '+506 6055-7788', 'andrea.salas@example.com', NULL),
    ('b2b2b2b2-0000-4000-8000-000000000003', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     '6-0150-0987', 'Pedro Pablo', 'Chacón Ureña', '1950-06-06',
     '2663-4455', NULL, 'Liberia, Guanacaste')
ON CONFLICT DO NOTHING;
