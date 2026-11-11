-- Demo data for agenda_db, consistent with seed/tenancy.sql.

INSERT INTO clinics (id, timezone)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'America/Costa_Rica'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'America/Costa_Rica')
ON CONFLICT (id) DO NOTHING;

INSERT INTO services (id, clinic_id, name, duration_minutes, specialty)
VALUES
    ('d1d1d1d1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Consulta general', 30, 'Medicina general'),
    ('d1d1d1d1-0000-4000-8000-000000000002', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     'Control de seguimiento', 20, 'Medicina general'),
    ('d2d2d2d2-0000-4000-8000-000000000001', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
     'Consulta general', 30, 'Medicina general')
ON CONFLICT (id) DO NOTHING;

INSERT INTO practitioners (id, clinic_id, user_id, user_subject, display_name, specialty)
VALUES
    ('c1c1c1c1-0000-4000-8000-000000000001', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
     '44444444-aaaa-4444-8444-444444444444', '44444444-4444-4444-8444-444444444444',
     'Dra. Ana Prueba', 'Medicina general')
ON CONFLICT (id) DO NOTHING;

INSERT INTO practitioner_services (clinic_id, practitioner_id, service_id)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000001',
     'd1d1d1d1-0000-4000-8000-000000000001'),
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'c1c1c1c1-0000-4000-8000-000000000001',
     'd1d1d1d1-0000-4000-8000-000000000002')
ON CONFLICT DO NOTHING;
