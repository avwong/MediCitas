INSERT INTO clinics (
    id,
    identifier,
    name,
    timezone
)
VALUES (
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'clinic-demo',
    'Clínica Demo',
    'America/Costa_Rica'
)
ON CONFLICT (id) DO NOTHING;


INSERT INTO users (
    id,
    keycloak_subject,
    clinic_id,
    role_code,
    active
)
VALUES
(
    '11111111-aaaa-4111-8111-111111111111',
    '11111111-1111-4111-8111-111111111111',
    NULL,
    'platform_admin',
    TRUE
),
(
    '22222222-aaaa-4222-8222-222222222222',
    '22222222-2222-4222-8222-222222222222',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'clinic_admin',
    TRUE
),
(
    '33333333-aaaa-4333-8333-333333333333',
    '33333333-3333-4333-8333-333333333333',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'reception',
    TRUE
),
(
    '44444444-aaaa-4444-8444-444444444444',
    '44444444-4444-4444-8444-444444444444',
    'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    'practitioner',
    TRUE
)
ON CONFLICT (id) DO NOTHING;