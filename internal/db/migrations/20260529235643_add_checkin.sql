-- Create "checkin" table
CREATE TABLE "checkin" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "patient_id" uuid NOT NULL, "mood" integer NOT NULL, "note" text NULL, "created_at" timestamptz NOT NULL DEFAULT now(), PRIMARY KEY ("id"), CONSTRAINT "checkin_patient_id_fkey" FOREIGN KEY ("patient_id") REFERENCES "patient_profile" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "checkin_mood_check" CHECK ((mood >= 1) AND (mood <= 5)));
-- Create index "idx_checkin_patient_date" to table: "checkin"
CREATE INDEX "idx_checkin_patient_date" ON "checkin" ("patient_id", "created_at" DESC);
