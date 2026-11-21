-- Sign-in uses stateless Bearer JWTs: there are no server-side login rows to
-- store, revoke or sweep. Existing rows are dropped with the table; clients
-- sign in again and receive a token.
DROP TABLE IF EXISTS logins;
