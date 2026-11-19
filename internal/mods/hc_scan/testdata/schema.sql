-- Test schema of the Auth database tables the hc_scan module reads and writes.
--
-- Column names and types were read from information_schema of a development copy of the real database on
-- 2026-11-19; only the columns this module or the Python HC code use are kept (the real tables have more, for
-- example t_mes_ee has gender, race, dob, email). It is used by repo_pg_test.go against a throw-away PostgreSQL,
-- never against a real database.

CREATE TABLE t_mqpd_hc_table (
    id                varchar PRIMARY KEY,
    process           varchar,
    cost_center       char(4),
    process_code      smallint,
    "type"            varchar,
    station_code      smallint,
    machine           varchar,
    budget_head_count real,
    update_time       timestamp,
    update_user       varchar,
    category          varchar,
    display_seq       smallint,
    hidden            boolean
);

CREATE TABLE t_mtr_checkpoint (
    uid               text PRIMARY KEY,
    row_id            bigint,
    training_record_id text,
    emp_code          text,
    emp_name          text,
    section_code      text,
    checkpoint_key    text,
    proficiency_level smallint,
    assessed_date     date,
    assessed_by_emp_code text,
    version_number    smallint,
    cim_machine_id    text,
    station_code      text,
    machine           text,
    cim_is_active     boolean,
    cim_process       text,
    event_source      text,
    created_at        timestamp,
    updated_at        timestamp,
    is_hidden         boolean
);

CREATE TABLE t_mes_ee (
    uid           varchar PRIMARY KEY,
    effective_mth date,
    ee_code       varchar,
    ee_name       varchar,
    dob           date,
    join_date     date,
    ee_category   text,
    job_grade     varchar,
    "group"       varchar,
    division      varchar,
    dept          varchar,
    section       varchar,
    cost_center   char(4),
    shift         text,
    resign_date   date,
    update_time   timestamp,
    direct_type   text,
    email         text
);

CREATE TABLE t_daily_attn_member (
    ee_code        varchar,
    ee_name        text,
    process        text,
    shift          char(2),
    direct_type    text,
    effective_date date,
    update_time    timestamp,
    update_user    text,
    uid            char(17) PRIMARY KEY,
    end_date       date,
    hidden         boolean
);

CREATE TABLE t_daily_attn_calendar (
    prodn_date  date PRIMARY KEY,
    week_day    char(3),
    "N"         text,
    "PA"        text,
    "PB"        text,
    "RA"        text,
    "RB"        text,
    "RC"        text,
    "RD"        text,
    "RE"        text,
    "RF"        text,
    "RG"        text,
    "RH"        text,
    "RI"        text,
    "RJ"        text,
    "RK"        text,
    "RL"        text,
    "TA"        text,
    "TB"        text,
    "TC"        text,
    update_time timestamp,
    "PC"        text,
    "PD"        text,
    "KA"        text,
    "KB"        text,
    "KC"        text,
    "KD"        text,
    "LA"        text,
    "LB"        text,
    "LC"        text,
    "LD"        text
);

CREATE TABLE t_daily_attn_data (
    uid             varchar PRIMARY KEY,
    ee_code         varchar,
    prodn_date      date,
    "shift"         char(2),
    schedule_shift  text,
    actual_shift    text,
    machine_hc_json jsonb,
    remark          text,
    build_in_ot     real,
    addt_ot         real,
    total_ot        real,
    hidden          boolean,
    update_time     timestamp,
    update_user     text
);
