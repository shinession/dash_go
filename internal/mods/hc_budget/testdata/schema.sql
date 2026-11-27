-- Test schema of t_mqpd_hc_table and "T_USER_ALL" used by hc_budget PostgreSQL integration tests (repo_pg_test.go).
-- Used only against a throw-away test database schema (hcbudget_it).

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

CREATE TABLE "T_USER_ALL" (
    ee_code          varchar,
    "HashID"         varchar,
    "Email"          varchar,
    "Name"           varchar,
    "HashPassword"   varchar,
    "Type"           varchar,
    "UpdateDate"     timestamp,
    phone_num        varchar,
    activated        boolean,
    is_company_email boolean
);
