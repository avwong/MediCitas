-- Agenda: índices para la búsqueda de pacientes por prefijo (RF14).
--
-- El listado de pacientes busca con LIKE 'texto%' sobre lower(identification),
-- lower(first_name) y lower(last_name). Un índice B-tree normal solo sirve a
-- LIKE si la base usa la intercalación C, y la imagen de PostgreSQL crea las
-- bases con en_US.utf8. text_pattern_ops compara carácter por carácter, así
-- que sirve a LIKE con cualquier intercalación.
--
-- Hace falta un índice por columna porque la búsqueda une las tres con OR: el
-- planificador combina los tres con un BitmapOr. Medido con 60 000 pacientes
-- en una clínica, la búsqueda pasa de recorrer todos los pacientes de la
-- clínica (198 ms) a leer solo los que coinciden (0,1 ms).
--
-- idx_patients_clinic_name se conserva: sigue cubriendo el listado sin texto
-- de búsqueda, que sale ordenado por apellido y nombre. Un índice con
-- text_pattern_ops no puede dar ese orden.

CREATE INDEX idx_patients_clinic_identification_prefix
    ON patients (clinic_id, lower(identification) text_pattern_ops);

CREATE INDEX idx_patients_clinic_first_name_prefix
    ON patients (clinic_id, lower(first_name) text_pattern_ops);

CREATE INDEX idx_patients_clinic_last_name_prefix
    ON patients (clinic_id, lower(last_name) text_pattern_ops);
