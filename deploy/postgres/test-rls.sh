#!/bin/sh
# Prueba de integración de RLS en agenda_db (segunda capa de I5)
#
# Demuestra que el motor aísla las clínicas aunque la consulta no filtre
# Todas las lecturas se hacen SIN WHERE de clínica, con el rol real de la aplicación
# Las escrituras que se intentan hacia otra clínica no dejan ningún cambio
set -eu

HOST="${PGHOST:-postgres}"

: "${AGENDA_DB_OWNER_PASSWORD:?required}"
: "${AGENDA_DB_APP_PASSWORD:?required}"
: "${AGENDA_DB_READONLY_PASSWORD:?required}"

# Tablas de Agenda con datos de clínica
# clinics usa id, y las demás usan clinic_id
TABLES="clinics specialties services practitioners practitioner_services patients schedules blocks appointments"

column_of() {
  if [ "$1" = "clinics" ]; then
    echo id
  else
    echo clinic_id
  fi
}

# Consulta como el dueño, que no pasa por RLS
owner_sql() {
  PGPASSWORD="$AGENDA_DB_OWNER_PASSWORD" \
    psql -X -q -At -v ON_ERROR_STOP=1 -h "$HOST" -U agenda_owner -d agenda_db "$@"
}

# Consulta con el rol de la aplicación
# El primer parámetro es la clínica de la sesión, o vacío para no fijar ninguna
app_sql() {
  clinic="$1"
  shift

  PGPASSWORD="$AGENDA_DB_APP_PASSWORD" \
    psql -X -q -At -v ON_ERROR_STOP=1 -h "$HOST" -U agenda_app -d agenda_db \
    -c "SELECT set_config('app.clinic_id', '$clinic', false)" \
    "$@"
}

readonly_sql() {
  clinic="$1"
  shift

  PGPASSWORD="$AGENDA_DB_READONLY_PASSWORD" \
    psql -X -q -At -v ON_ERROR_STOP=1 -h "$HOST" -U agenda_readonly -d agenda_db \
    -c "SELECT set_config('app.clinic_id', '$clinic', false)" \
    "$@"
}

pass() {
  echo "ok - $1"
}

fail() {
  echo "FAIL: $1"
  exit 1
}

# Espera que la sentencia falle con una violación de RLS
expect_rls_error() {
  description="$1"
  clinic="$2"
  sql="$3"

  if output="$(app_sql "$clinic" -c "$sql" 2>&1)"; then
    fail "$description tuvo éxito: $output"
  fi

  case "$output" in
    *"row-level security"*) pass "$description" ;;
    *) fail "$description falló por otra razón: $output" ;;
  esac
}

# Espera que la sentencia no toque ninguna fila
expect_no_rows() {
  description="$1"
  clinic="$2"
  sql="$3"

  rows="$(app_sql "$clinic" -c "$sql" | tail -n +2)"

  if [ -n "$rows" ]; then
    fail "$description afectó filas: $rows"
  fi

  pass "$description"
}

echo "== Configuración =="

# Los roles de la aplicación no omiten RLS y no son dueños de las tablas
for role in agenda_app agenda_readonly; do
  bypass="$(owner_sql -c "SELECT rolbypassrls FROM pg_roles WHERE rolname = '$role'")"
  [ "$bypass" = "f" ] || fail "$role omite RLS"
done

pass "agenda_app y agenda_readonly son NOBYPASSRLS"

for table in $TABLES; do
  enabled="$(owner_sql -c "SELECT relrowsecurity FROM pg_class WHERE oid = 'public.${table}'::regclass")"
  owner="$(owner_sql -c "SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = '$table'")"

  [ "$enabled" = "t" ] || fail "$table no tiene RLS activado"
  [ "$owner" = "agenda_owner" ] || fail "$table es de $owner, no de agenda_owner"
done

pass "las 9 tablas con datos de clínica tienen RLS y son de agenda_owner"

# audit_outbox es la excepción documentada: el relay la lee de todas las clínicas
outbox="$(owner_sql -c "SELECT relrowsecurity FROM pg_class WHERE oid = 'public.audit_outbox'::regclass")"
[ "$outbox" = "f" ] || fail "audit_outbox no debía tener RLS"

pass "audit_outbox queda fuera de RLS, como excepción documentada"

echo "== Sin clínica no se ve nada =="

for table in $TABLES; do
  seen="$(app_sql "" -c "SELECT count(*) FROM $table" | tail -n 1)"
  [ "$seen" = "0" ] || fail "sin app.clinic_id, $table muestra $seen filas"
done

pass "sin app.clinic_id, el rol de la aplicación no ve ninguna fila de ninguna tabla"

null_clinic="$(app_sql "" -c "SELECT current_clinic_id() IS NULL" | tail -n 1)"
[ "$null_clinic" = "t" ] || fail "current_clinic_id() debía ser NULL sin clínica"

pass "current_clinic_id() es NULL sin clínica"

echo "== Cada clínica ve exactamente lo suyo, aunque la consulta no filtre =="

clinics="$(owner_sql -c "SELECT id FROM clinics ORDER BY id")"
[ -n "$clinics" ] || fail "no hay clínicas: falta la semilla"

# La suma de lo que ve cada clínica debe ser el total real de la tabla
for table in $TABLES; do
  column="$(column_of "$table")"
  total="$(owner_sql -c "SELECT count(*) FROM $table")"
  sum=0

  for clinic in $clinics; do
    expected="$(owner_sql -c "SELECT count(*) FROM $table WHERE $column = '$clinic'")"
    seen="$(app_sql "$clinic" -c "SELECT count(*) FROM $table" | tail -n 1)"

    [ "$seen" = "$expected" ] || fail "$table: la clínica $clinic ve $seen filas y tiene $expected"

    sum=$((sum + seen))
  done

  [ "$sum" = "$total" ] || fail "$table: las clínicas suman $sum filas y la tabla tiene $total"
done

pass "en las 9 tablas, cada clínica ve solo sus filas y entre todas suman el total"

# Que la prueba no sea vacía: la semilla trae pacientes y citas
for table in patients appointments; do
  [ "$(owner_sql -c "SELECT count(*) FROM $table")" -gt 0 ] || fail "$table está vacía: la prueba no demuestra nada"
done

pass "la prueba no es vacía: hay pacientes y citas en varias clínicas"

# Las dos clínicas de la semilla con las que se prueban las escrituras
demo="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
other="bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

for clinic in "$demo" "$other"; do
  echo "$clinics" | grep -q "$clinic" || fail "falta la clínica $clinic de la semilla"
done

echo "== Las escrituras hacia otra clínica se rechazan =="

expect_rls_error \
  "insertar un paciente en otra clínica" \
  "$demo" \
  "INSERT INTO patients (clinic_id, identification, first_name, last_name, birth_date) VALUES ('$other', 'fuga-rls', 'Fuga', 'Prueba', '2000-01-01')"

expect_rls_error \
  "insertar un paciente sin clínica en la sesión" \
  "" \
  "INSERT INTO patients (clinic_id, identification, first_name, last_name, birth_date) VALUES ('$demo', 'fuga-rls', 'Fuga', 'Prueba', '2000-01-01')"

expect_rls_error \
  "mover un paciente propio hacia otra clínica" \
  "$demo" \
  "UPDATE patients SET clinic_id = '$other' WHERE id = (SELECT id FROM patients LIMIT 1)"

expect_no_rows \
  "modificar pacientes de otra clínica no toca ninguna fila" \
  "$demo" \
  "UPDATE patients SET phone = '0000' WHERE clinic_id = '$other' RETURNING id"

expect_no_rows \
  "borrar bloqueos y horarios de otra clínica no toca ninguna fila" \
  "$demo" \
  "DELETE FROM schedules WHERE clinic_id = '$other' RETURNING id"

expect_no_rows \
  "cancelar citas de otra clínica no toca ninguna fila" \
  "$demo" \
  "UPDATE appointments SET status = 'cancelled' WHERE clinic_id = '$other' RETURNING id"

# Nada de lo anterior dejó cambios
leftover="$(owner_sql -c "SELECT count(*) FROM patients WHERE identification = 'fuga-rls' OR phone = '0000'")"
[ "$leftover" = "0" ] || fail "las pruebas dejaron $leftover filas modificadas"

pass "ninguna de esas escrituras dejó cambios"

echo "== El rol de solo lectura también queda aislado =="

seen="$(readonly_sql "" -c "SELECT count(*) FROM patients" | tail -n 1)"
[ "$seen" = "0" ] || fail "agenda_readonly sin clínica ve $seen pacientes"

expected="$(owner_sql -c "SELECT count(*) FROM patients WHERE clinic_id = '$demo'")"
seen="$(readonly_sql "$demo" -c "SELECT count(*) FROM patients" | tail -n 1)"
[ "$seen" = "$expected" ] || fail "agenda_readonly ve $seen pacientes y la clínica tiene $expected"

pass "agenda_readonly sin clínica no ve nada, y con clínica ve solo la suya"

echo "== La única excepción: practitioner_clinic_of_user =="

user_id="$(owner_sql -c "SELECT user_id FROM practitioners WHERE clinic_id = '$demo' LIMIT 1")"
[ -n "$user_id" ] || fail "la clínica $demo no tiene profesionales"

resolved="$(app_sql "" -c "SELECT practitioner_clinic_of_user('$user_id')" | tail -n 1)"
[ "$resolved" = "$demo" ] || fail "la función devolvió $resolved, want $demo"

pass "la función devuelve la clínica del profesional sin necesitar una clínica en la sesión"

# La función no abre las filas: sigue sin verse nada en la tabla
visible="$(app_sql "" -c "SELECT practitioner_clinic_of_user('$user_id')" -c "SELECT count(*) FROM practitioners" | tail -n 1)"
[ "$visible" = "0" ] || fail "tras llamar a la función, practitioners muestra $visible filas"

pass "llamar a la función no abre la tabla practitioners"

if readonly_sql "" -c "SELECT practitioner_clinic_of_user('$user_id')" >/tmp/rls-test.out 2>&1; then
  fail "agenda_readonly pudo ejecutar practitioner_clinic_of_user"
fi

pass "agenda_readonly no puede ejecutar la función"

echo
echo "All Row-Level Security checks passed."
