-- Demo data for tenancy_db. Every user matches a Keycloak user with the same
-- subject in keycloak/realm-export.json.
--
-- Two clinics so that isolation between clinics (I5) can be exercised from
-- the start. The data-model owner extends this to at least three clinics.

INSERT INTO clinics (id, identifier, name, timezone, status)
VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'clinic-demo', 'Clínica Demo', 'America/Costa_Rica', 'active'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'clinic-norte', 'Clínica Norte', 'America/Costa_Rica', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, keycloak_subject, username, clinic_id, role_code, active)
VALUES
    ('11111111-aaaa-4111-8111-111111111111', '11111111-1111-4111-8111-111111111111',
     'platform.admin', NULL, 'platform_admin', TRUE),
    ('22222222-aaaa-4222-8222-222222222222', '22222222-2222-4222-8222-222222222222',
     'clinic.admin', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'clinic_admin', TRUE),
    ('33333333-aaaa-4333-8333-333333333333', '33333333-3333-4333-8333-333333333333',
     'reception', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'reception', TRUE),
    ('44444444-aaaa-4444-8444-444444444444', '44444444-4444-4444-8444-444444444444',
     'practitioner', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'practitioner', TRUE),
    ('55555555-aaaa-4555-8555-555555555555', '55555555-5555-4555-8555-555555555555',
     'norte.admin', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'clinic_admin', TRUE),
    ('66666666-aaaa-4666-8666-666666666666', '66666666-6666-4666-8666-666666666666',
     'norte.reception', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'reception', TRUE)
ON CONFLICT (id) DO NOTHING;
