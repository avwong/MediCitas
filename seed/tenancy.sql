-- Datos de ejemplo para tenancy_db.
--
-- Cada usuario coincide con un usuario de Keycloak que tiene el mismo subject;
-- los usuarios de Keycloak están en keycloak/realm-export.json.
--
-- Hay tres clínicas, para poder probar el aislamiento entre clínicas (I5).
-- Cada una tiene su administrador, su recepción y tres profesionales. La
-- tercera usa otra zona horaria, para probar que cada clínica respeta la suya.

INSERT INTO clinics (id, identifier, name, timezone, status)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'clinic-demo', 'Clínica Demo', 'America/Costa_Rica', 'active'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'clinic-norte', 'Clínica Norte', 'America/Costa_Rica', 'active'),
    ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'clinic-sur', 'Clínica Sur', 'America/Bogota', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, keycloak_subject, username, clinic_id, role_code, active)
VALUES
    -- Plataforma
    ('11111111-aaaa-4111-8111-111111111111', '11111111-1111-4111-8111-111111111111',
     'platform.admin', NULL, 'platform_admin', TRUE),

    -- Clínica Demo
    ('22222222-aaaa-4222-8222-222222222222', '22222222-2222-4222-8222-222222222222',
     'clinic.admin', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'clinic_admin', TRUE),
    ('33333333-aaaa-4333-8333-333333333333', '33333333-3333-4333-8333-333333333333',
     'reception', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'reception', TRUE),
    ('44444444-aaaa-4444-8444-444444444444', '44444444-4444-4444-8444-444444444444',
     'practitioner', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'practitioner', TRUE),
    ('77777777-aaaa-4777-8777-777777777777', '77777777-7777-4777-8777-777777777777',
     'practitioner2', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'practitioner', TRUE),
    ('88888888-aaaa-4888-8888-888888888888', '88888888-8888-4888-8888-888888888888',
     'practitioner3', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'practitioner', TRUE),

    -- Clínica Norte
    ('55555555-aaaa-4555-8555-555555555555', '55555555-5555-4555-8555-555555555555',
     'norte.admin', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'clinic_admin', TRUE),
    ('66666666-aaaa-4666-8666-666666666666', '66666666-6666-4666-8666-666666666666',
     'norte.reception', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'reception', TRUE),
    ('99999999-aaaa-4999-8999-999999999999', '99999999-9999-4999-8999-999999999999',
     'norte.practitioner1', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'practitioner', TRUE),
    ('10101010-aaaa-4101-8101-101010101010', '10101010-1010-4101-8101-101010101010',
     'norte.practitioner2', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'practitioner', TRUE),
    ('20202020-aaaa-4202-8202-202020202020', '20202020-2020-4202-8202-202020202020',
     'norte.practitioner3', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'practitioner', TRUE),

    -- Clínica Sur
    ('30303030-aaaa-4303-8303-303030303030', '30303030-3030-4303-8303-303030303030',
     'sur.admin', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'clinic_admin', TRUE),
    ('40404040-aaaa-4404-8404-404040404040', '40404040-4040-4404-8404-404040404040',
     'sur.reception', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'reception', TRUE),
    ('50505050-aaaa-4505-8505-505050505050', '50505050-5050-4505-8505-505050505050',
     'sur.practitioner1', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'practitioner', TRUE),
    ('60606060-aaaa-4606-8606-606060606060', '60606060-6060-4606-8606-606060606060',
     'sur.practitioner2', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'practitioner', TRUE),
    ('70707070-aaaa-4707-8707-707070707070', '70707070-7070-4707-8707-707070707070',
     'sur.practitioner3', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'practitioner', TRUE)
ON CONFLICT (id) DO NOTHING;
