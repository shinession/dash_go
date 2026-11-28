-- Minimal scratch schema matching Log.t_log_daily and Auth."T_USER_ALL" for web_log integration tests.

CREATE TABLE t_log_daily (
    uid         varchar(64) PRIMARY KEY,
    log_date    date NOT NULL,
    ip_addr     inet NOT NULL,
    app         varchar(20),
    category    varchar(80),
    host        varchar(64),
    action_count integer NOT NULL,
    update_time timestamp
);

CREATE INDEX idx_t_log_daily_date ON t_log_daily (log_date);

CREATE TABLE "T_USER_ALL" (
    ee_code      varchar(32),
    "Name"       varchar(120),
    "Type"       varchar(32),
    activated    boolean,
    "UpdateDate" timestamp
);
