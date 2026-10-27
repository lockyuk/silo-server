-- +goose Up
-- +goose StatementBegin
-- fcm_direct: Android pushes sent by this server straight to Firebase Cloud
-- Messaging with the operator's own Firebase project, for builds of the app
-- that are not registered with the Silo relay's project.
ALTER TABLE public.push_devices DROP CONSTRAINT push_devices_provider_check;
ALTER TABLE public.push_devices ADD CONSTRAINT push_devices_provider_check
    CHECK (provider IN ('silo_relay', 'fcm_direct'));
ALTER TABLE public.push_delivery_attempts DROP CONSTRAINT push_delivery_attempts_provider_check;
ALTER TABLE public.push_delivery_attempts ADD CONSTRAINT push_delivery_attempts_provider_check
    CHECK (provider IN ('silo_relay', 'fcm_direct'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.push_delivery_attempts WHERE provider = 'fcm_direct';
DELETE FROM public.push_devices WHERE provider = 'fcm_direct';
ALTER TABLE public.push_devices DROP CONSTRAINT push_devices_provider_check;
ALTER TABLE public.push_devices ADD CONSTRAINT push_devices_provider_check
    CHECK (provider IN ('silo_relay'));
ALTER TABLE public.push_delivery_attempts DROP CONSTRAINT push_delivery_attempts_provider_check;
ALTER TABLE public.push_delivery_attempts ADD CONSTRAINT push_delivery_attempts_provider_check
    CHECK (provider IN ('silo_relay'));
-- +goose StatementEnd
