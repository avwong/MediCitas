-- Agenda: aislamiento entre clínicas con Row-Level Security (RLS)
--
-- Es la segunda capa de I5, aparte del filtro clinic_id de cada consulta
-- Con el filtro, el aislamiento depende de que cada consulta lo incluya
-- Con RLS lo garantiza el motor, aunque una consulta olvide el filtro
--
-- La clínica de la sesión sale de app.clinic_id
-- TxManager lo fija con set_config dentro de cada transacción, y solo dura esa transacción
-- Sin app.clinic_id no se ve ninguna fila, así que la falla es hacia el lado seguro
--
-- Los roles de la aplicación no omiten RLS: son NOBYPASSRLS y no son dueños de las tablas
-- agenda_owner sí es dueño, y por eso no queda sujeto a las políticas
-- Solo se usa para migrar, y no se activa FORCE para no vaciar sin aviso una migración de datos futura
-- Las semillas corren como superusuario y tampoco pasan por las políticas

-- Clínica de la transacción actual
-- NULLIF convierte el valor vacío en NULL, y con NULL ninguna política deja pasar filas
CREATE FUNCTION current_clinic_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
    SELECT NULLIF(current_setting('app.clinic_id', true), '')::uuid
$$;

COMMENT ON FUNCTION current_clinic_id() IS
    'Clínica de la transacción, tomada de app.clinic_id. Sin valor devuelve NULL y las políticas de RLS no dejan pasar ninguna fila.';

-- Una política por tabla, con la misma regla para leer y para escribir
-- USING limita las filas que se ven, se actualizan o se borran
-- WITH CHECK limita las filas que se pueden insertar o dejar tras un UPDATE
-- Con WITH CHECK una escritura no puede crear ni mover filas hacia otra clínica

ALTER TABLE clinics ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON clinics
    USING (id = current_clinic_id())
    WITH CHECK (id = current_clinic_id());

ALTER TABLE specialties ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON specialties
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE services ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON services
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE practitioners ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON practitioners
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE practitioner_services ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON practitioner_services
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE patients ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON patients
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE schedules ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON schedules
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE blocks ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON blocks
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

ALTER TABLE appointments ENABLE ROW LEVEL SECURITY;

CREATE POLICY clinic_isolation ON appointments
    USING (clinic_id = current_clinic_id())
    WITH CHECK (clinic_id = current_clinic_id());

-- audit_outbox y audit_outbox_delivered quedan fuera de RLS a propósito
-- Son la cola que el relay lee de todas las clínicas para entregar a Auditoría
-- El relay no actúa en nombre de una clínica, así que con RLS no vería ningún evento
-- Esas tablas ya son append-only y solo llevan identificadores, nunca datos de contacto

-- Excepción deliberada: la clínica de un usuario que es profesional
-- Tenancy avisa a Agenda cuando un usuario deja de ser profesional o vuelve a serlo
-- Esa llamada interna trae el id del usuario pero no su clínica
-- Sin clínica no hay con qué fijar app.clinic_id, y con RLS el UPDATE no vería la fila
-- La función devuelve solo la clínica, y con ella la escritura sí corre bajo las políticas
-- SECURITY DEFINER corre como agenda_owner, que no pasa por RLS
-- search_path fijo evita que otro esquema sustituya la tabla
CREATE FUNCTION practitioner_clinic_of_user(p_user_id uuid)
RETURNS uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public
AS $$
    SELECT clinic_id FROM practitioners WHERE user_id = p_user_id
$$;

COMMENT ON FUNCTION practitioner_clinic_of_user(uuid) IS
    'Única excepción a RLS: devuelve la clínica del profesional de un usuario. Solo la usa la ruta interna con la que Tenancy activa o desactiva profesionales.';

REVOKE ALL ON FUNCTION practitioner_clinic_of_user(uuid) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION practitioner_clinic_of_user(uuid) TO agenda_app;
