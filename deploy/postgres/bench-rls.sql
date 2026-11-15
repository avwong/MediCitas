-- Medición de RLS contra el filtro clinic_id en agenda_db
--
-- Compara tres estrategias sobre las mismas seis consultas de la aplicación
--   B   filtro clinic_id sin RLS, con agenda_owner, que no pasa por las políticas
--   C1  filtro clinic_id y RLS, con agenda_app, que es lo que hace la aplicación hoy
--   C2  solo RLS, con agenda_app y sin ningún filtro de clínica en la consulta
--
-- Carga 100 clínicas con 30.000 pacientes, 2.000 profesionales y 200.000 citas
-- Mide la clínica 50 en 5 rondas, rotando el orden de las estrategias en cada una
-- La ronda 1 calienta la caché y no se cuenta; se reporta la mediana de las rondas 2 a 5
--
-- Todo corre en una transacción que se deshace al final, así que no deja datos
-- Los datos de la semilla no se tocan: las clínicas de prueba tienen otros ids
--
-- Uso, con la pila levantada y desde la raíz del repositorio
-- La variable POSTGRES_USER se lee dentro del contenedor
--   docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d agenda_db -X' < deploy/postgres/bench-rls.sql
-- En PowerShell
--   Get-Content deploy/postgres/bench-rls.sql | docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d agenda_db -X'

\set ON_ERROR_STOP on
\timing off
BEGIN;
SET LOCAL synchronous_commit = off;

-- ---------------------------------------------------------------- datos
INSERT INTO clinics (id, timezone)
SELECT ('d0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid, 'UTC' FROM generate_series(1, 100) g;

INSERT INTO specialties (id, clinic_id, name)
SELECT ('e0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid,
       ('d0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid, 'General'
FROM generate_series(1, 100) g;

INSERT INTO services (id, clinic_id, name, duration_minutes, specialty_id)
SELECT ('f0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid,
       ('d0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid, 'Consulta', 30,
       ('e0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid
FROM generate_series(1, 100) g;

INSERT INTO practitioners (id, clinic_id, user_id, user_subject, display_name, specialty_id)
SELECT ('c0000000-0000-4000-8000-' || lpad((g * 100 + p)::text, 12, '0'))::uuid,
       ('d0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid,
       gen_random_uuid(), 'sub-' || g || '-' || p, 'Profesional ' || g || '-' || p,
       ('e0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid
FROM generate_series(1, 100) g, generate_series(0, 19) p;

INSERT INTO patients (id, clinic_id, identification, first_name, last_name, birth_date)
SELECT ('b0000000-0000-4000-8000-' || lpad((g * 1000 + n)::text, 12, '0'))::uuid,
       ('d0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid,
       'ID-' || g || '-' || n, 'Nombre' || (n % 211), 'Apellido' || (n % 97) || '-' || n,
       DATE '1970-01-01' + (n * 17)
FROM generate_series(1, 100) g, generate_series(0, 299) n;

-- 100 citas por profesional: 10 por dia durante 10 dias, sin solaparse
INSERT INTO appointments (clinic_id, patient_id, practitioner_id, service_id, starts_at, ends_at,
                          status, created_by, closed_by, closed_at, cancellation_reason)
SELECT ('d0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid,
       ('b0000000-0000-4000-8000-' || lpad((g * 1000 + ((p * 13 + k) % 300))::text, 12, '0'))::uuid,
       ('c0000000-0000-4000-8000-' || lpad((g * 100 + p)::text, 12, '0'))::uuid,
       ('f0000000-0000-4000-8000-' || lpad(g::text, 12, '0'))::uuid,
       TIMESTAMPTZ '2026-12-01 08:00+00' + ((k / 10) * INTERVAL '1 day') + ((k % 10) * INTERVAL '30 minutes'),
       TIMESTAMPTZ '2026-12-01 08:00+00' + ((k / 10) * INTERVAL '1 day') + ((k % 10) * INTERVAL '30 minutes'),
       CASE WHEN k % 10 < 6 THEN 'booked' WHEN k % 10 = 6 THEN 'attended' WHEN k % 10 = 7 THEN 'no_show'
            WHEN k % 10 = 8 THEN 'cancelled' ELSE 'rescheduled' END,
       'bench',
       CASE WHEN k % 10 < 6 THEN NULL ELSE 'bench' END,
       CASE WHEN k % 10 < 6 THEN NULL
            ELSE TIMESTAMPTZ '2026-12-01 08:00+00' + ((k / 10) * INTERVAL '1 day') + ((k % 10) * INTERVAL '30 minutes') + INTERVAL '1 hour' END,
       CASE WHEN k % 10 = 8 THEN 'bench' ELSE NULL END
FROM generate_series(1, 100) g, generate_series(0, 19) p, generate_series(0, 99) k;

ANALYZE clinics; ANALYZE specialties; ANALYZE services; ANALYZE practitioners; ANALYZE patients; ANALYZE appointments;

SELECT 'clinicas' AS tabla, count(*) FROM clinics WHERE id::text LIKE 'd0000000%'
UNION ALL SELECT 'pacientes', count(*) FROM patients WHERE clinic_id::text LIKE 'd0000000%'
UNION ALL SELECT 'profesionales', count(*) FROM practitioners WHERE clinic_id::text LIKE 'd0000000%'
UNION ALL SELECT 'citas', count(*) FROM appointments WHERE clinic_id::text LIKE 'd0000000%';

-- ---------------------------------------------------------------- cronometro
-- Ejecuta la consulta n veces con el rol de la sesion y devuelve los milisegundos promedio
-- Cada ejecucion se planifica de nuevo, como una consulta normal de la aplicacion
CREATE FUNCTION public.bench(q text, n int) RETURNS numeric
LANGUAGE plpgsql AS $$
DECLARE t0 timestamptz; i int;
BEGIN
    t0 := clock_timestamp();
    FOR i IN 1..n LOOP
        EXECUTE q;
    END LOOP;
    RETURN round((extract(epoch FROM clock_timestamp() - t0) * 1000 / n)::numeric, 3);
END
$$;

-- Estrategia B: filtro clinic_id sin RLS (agenda_owner no pasa por las politicas)
-- Estrategia C1: filtro clinic_id y RLS (lo que hace la aplicacion hoy)
-- Estrategia C2: solo RLS, sin ningun filtro de clinica en la consulta
SELECT set_config('app.clinic_id', 'd0000000-0000-4000-8000-000000000050', true);

CREATE TEMP TABLE resultados (ronda int, consulta text, estrategia text, ms numeric) ON COMMIT DROP;
GRANT ALL ON resultados TO PUBLIC;

-- ronda 1: Q1 leer un paciente por id
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 1, 'Q1 leer un paciente por id', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q1 leer un paciente por id', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q1 leer un paciente por id', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;

-- ronda 1: Q2 listado de pacientes (50, por nombre)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 1, 'Q2 listado de pacientes (50, por nombre)', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q2 listado de pacientes (50, por nombre)', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q2 listado de pacientes (50, por nombre)', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;

-- ronda 1: Q3 agenda del dia de toda la clinica (RF20)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 1, 'Q3 agenda del dia de toda la clinica (RF20)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q3 agenda del dia de toda la clinica (RF20)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q3 agenda del dia de toda la clinica (RF20)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;

-- ronda 1: Q4 historial de un paciente (RF16)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 1, 'Q4 historial de un paciente (RF16)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q4 historial de un paciente (RF16)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q4 historial de un paciente (RF16)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;

-- ronda 1: Q5 conteo por estado en 10 dias (RF21)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 1, 'Q5 conteo por estado en 10 dias (RF21)', 'B', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q5 conteo por estado en 10 dias (RF21)', 'C1', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q5 conteo por estado en 10 dias (RF21)', 'C2', public.bench('SELECT status, count(*) FROM appointments WHERE starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;

-- ronda 1: Q6 citas reservadas de un profesional en una franja (RF7)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 1, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'B', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C1', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 1, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C2', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;

-- ronda 2: Q1 leer un paciente por id
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q1 leer un paciente por id', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q1 leer un paciente por id', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 2, 'Q1 leer un paciente por id', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;

-- ronda 2: Q2 listado de pacientes (50, por nombre)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q2 listado de pacientes (50, por nombre)', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q2 listado de pacientes (50, por nombre)', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 2, 'Q2 listado de pacientes (50, por nombre)', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;

-- ronda 2: Q3 agenda del dia de toda la clinica (RF20)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q3 agenda del dia de toda la clinica (RF20)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q3 agenda del dia de toda la clinica (RF20)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 2, 'Q3 agenda del dia de toda la clinica (RF20)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;

-- ronda 2: Q4 historial de un paciente (RF16)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q4 historial de un paciente (RF16)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q4 historial de un paciente (RF16)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 2, 'Q4 historial de un paciente (RF16)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;

-- ronda 2: Q5 conteo por estado en 10 dias (RF21)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q5 conteo por estado en 10 dias (RF21)', 'C1', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q5 conteo por estado en 10 dias (RF21)', 'C2', public.bench('SELECT status, count(*) FROM appointments WHERE starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 2, 'Q5 conteo por estado en 10 dias (RF21)', 'B', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;

-- ronda 2: Q6 citas reservadas de un profesional en una franja (RF7)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C1', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 2, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C2', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 2, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'B', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;

-- ronda 3: Q1 leer un paciente por id
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q1 leer un paciente por id', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 3, 'Q1 leer un paciente por id', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q1 leer un paciente por id', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;

-- ronda 3: Q2 listado de pacientes (50, por nombre)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q2 listado de pacientes (50, por nombre)', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 3, 'Q2 listado de pacientes (50, por nombre)', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q2 listado de pacientes (50, por nombre)', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;

-- ronda 3: Q3 agenda del dia de toda la clinica (RF20)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q3 agenda del dia de toda la clinica (RF20)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 3, 'Q3 agenda del dia de toda la clinica (RF20)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q3 agenda del dia de toda la clinica (RF20)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;

-- ronda 3: Q4 historial de un paciente (RF16)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q4 historial de un paciente (RF16)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 3, 'Q4 historial de un paciente (RF16)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q4 historial de un paciente (RF16)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;

-- ronda 3: Q5 conteo por estado en 10 dias (RF21)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q5 conteo por estado en 10 dias (RF21)', 'C2', public.bench('SELECT status, count(*) FROM appointments WHERE starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 3, 'Q5 conteo por estado en 10 dias (RF21)', 'B', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q5 conteo por estado en 10 dias (RF21)', 'C1', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;

-- ronda 3: Q6 citas reservadas de un profesional en una franja (RF7)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C2', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 3, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'B', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 3, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C1', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;

-- ronda 4: Q1 leer un paciente por id
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 4, 'Q1 leer un paciente por id', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q1 leer un paciente por id', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q1 leer un paciente por id', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;

-- ronda 4: Q2 listado de pacientes (50, por nombre)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 4, 'Q2 listado de pacientes (50, por nombre)', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q2 listado de pacientes (50, por nombre)', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q2 listado de pacientes (50, por nombre)', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;

-- ronda 4: Q3 agenda del dia de toda la clinica (RF20)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 4, 'Q3 agenda del dia de toda la clinica (RF20)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q3 agenda del dia de toda la clinica (RF20)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q3 agenda del dia de toda la clinica (RF20)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;

-- ronda 4: Q4 historial de un paciente (RF16)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 4, 'Q4 historial de un paciente (RF16)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q4 historial de un paciente (RF16)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q4 historial de un paciente (RF16)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;

-- ronda 4: Q5 conteo por estado en 10 dias (RF21)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 4, 'Q5 conteo por estado en 10 dias (RF21)', 'B', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q5 conteo por estado en 10 dias (RF21)', 'C2', public.bench('SELECT status, count(*) FROM appointments WHERE starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q5 conteo por estado en 10 dias (RF21)', 'C1', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;

-- ronda 4: Q6 citas reservadas de un profesional en una franja (RF7)
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 4, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'B', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C2', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 4, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C1', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;

-- ronda 5: Q1 leer un paciente por id
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q1 leer un paciente por id', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q1 leer un paciente por id', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 5, 'Q1 leer un paciente por id', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND id = ''b0000000-0000-4000-8000-000000050017''', 500);
RESET ROLE;

-- ronda 5: Q2 listado de pacientes (50, por nombre)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q2 listado de pacientes (50, por nombre)', 'C2', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q2 listado de pacientes (50, por nombre)', 'C1', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 5, 'Q2 listado de pacientes (50, por nombre)', 'B', public.bench('SELECT id, identification, first_name, last_name FROM patients WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND true ORDER BY lower(last_name), lower(first_name), id LIMIT 50', 500);
RESET ROLE;

-- ronda 5: Q3 agenda del dia de toda la clinica (RF20)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q3 agenda del dia de toda la clinica (RF20)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q3 agenda del dia de toda la clinica (RF20)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 5, 'Q3 agenda del dia de toda la clinica (RF20)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, p.first_name, p.last_name, pr.display_name, s.name FROM appointments a JOIN patients p ON p.clinic_id = a.clinic_id AND p.id = a.patient_id JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.starts_at >= ''2026-12-03 00:00+00'' AND a.starts_at < ''2026-12-04 00:00+00'' ORDER BY a.starts_at, pr.display_name, a.id', 100);
RESET ROLE;

-- ronda 5: Q4 historial de un paciente (RF16)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q4 historial de un paciente (RF16)', 'C2', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q4 historial de un paciente (RF16)', 'C1', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 5, 'Q4 historial de un paciente (RF16)', 'B', public.bench('SELECT a.id, a.starts_at, a.status, pr.display_name, s.name FROM appointments a JOIN practitioners pr ON pr.clinic_id = a.clinic_id AND pr.id = a.practitioner_id JOIN services s ON s.clinic_id = a.clinic_id AND s.id = a.service_id WHERE a.clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND a.patient_id = ''b0000000-0000-4000-8000-000000050017'' ORDER BY a.starts_at DESC, a.id LIMIT 50', 300);
RESET ROLE;

-- ronda 5: Q5 conteo por estado en 10 dias (RF21)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q5 conteo por estado en 10 dias (RF21)', 'C2', public.bench('SELECT status, count(*) FROM appointments WHERE starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q5 conteo por estado en 10 dias (RF21)', 'C1', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 5, 'Q5 conteo por estado en 10 dias (RF21)', 'B', public.bench('SELECT status, count(*) FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND starts_at >= ''2026-12-01 00:00+00'' AND starts_at < ''2026-12-11 00:00+00'' GROUP BY status', 100);
RESET ROLE;

-- ronda 5: Q6 citas reservadas de un profesional en una franja (RF7)
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C2', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_app;
INSERT INTO resultados SELECT 5, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'C1', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;
SET LOCAL ROLE agenda_owner;
INSERT INTO resultados SELECT 5, 'Q6 citas reservadas de un profesional en una franja (RF7)', 'B', public.bench('SELECT id, starts_at, ends_at FROM appointments WHERE clinic_id = ''d0000000-0000-4000-8000-000000000050'' AND practitioner_id = ''c0000000-0000-4000-8000-000000005007'' AND status = ''booked'' AND starts_at < ''2026-12-03 18:00+00'' AND ends_at > ''2026-12-03 08:00+00''', 500);
RESET ROLE;

\echo
\echo ===== MEDIANA de las rondas 2 a 5, en milisegundos por consulta (la ronda 1 calienta la cache)
SELECT consulta,
       round((percentile_cont(0.5) WITHIN GROUP (ORDER BY ms) FILTER (WHERE estrategia = 'B'))::numeric, 3)  AS "B filtro",
       round((percentile_cont(0.5) WITHIN GROUP (ORDER BY ms) FILTER (WHERE estrategia = 'C1'))::numeric, 3) AS "C1 RLS+filtro",
       round((percentile_cont(0.5) WITHIN GROUP (ORDER BY ms) FILTER (WHERE estrategia = 'C2'))::numeric, 3) AS "C2 solo RLS"
FROM resultados WHERE ronda >= 2 GROUP BY consulta ORDER BY consulta;

\echo ===== MINIMO y MAXIMO de cada estrategia en las mismas rondas
SELECT consulta, estrategia, min(ms) AS minimo, max(ms) AS maximo
FROM resultados WHERE ronda >= 2 GROUP BY consulta, estrategia ORDER BY consulta, estrategia;

\echo ===== SUMA de las medianas de las 6 consultas (un recorrido tipico)
SELECT estrategia, round(sum(mediana)::numeric, 3) AS total_ms FROM (
  SELECT consulta, estrategia, percentile_cont(0.5) WITHIN GROUP (ORDER BY ms) AS mediana
  FROM resultados WHERE ronda >= 2 GROUP BY consulta, estrategia) m GROUP BY estrategia ORDER BY estrategia;

ROLLBACK;
