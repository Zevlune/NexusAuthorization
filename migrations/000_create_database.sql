-- Run once as a database administrator, connected to the postgres database.
-- CREATE DATABASE must run outside a transaction. If the application uses a
-- separate database role, assign database ownership to it after creation.
CREATE DATABASE "nexus-authorization" WITH TEMPLATE template0 ENCODING 'UTF8';
