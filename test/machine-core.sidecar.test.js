/* eslint-env jest */

const {
    shouldUseGoMachineCoreSidecar,
    shouldUseGoMachineCorePath,
    extractSessionID,
    emitReplayEventToSocket,
    buildLegacyWorkflowState,
    buildLegacySenderStatus,
    buildLegacyHomingState,
    extractControllerType,
    buildCanonicalReplayEvent,
    buildCanonicalSnapshot,
    relayMachineSessionSnapshot,
    relayMachineSessionEvent,
} = require('../src/server/services/machine-core/sidecar');

describe('machine-core sidecar helpers', () => {
    test('enables sidecar only for go transport modes', () => {
        expect(shouldUseGoMachineCoreSidecar({ MACHINE_CORE_TRANSPORT: 'grpc' })).toBe(true);
        expect(shouldUseGoMachineCoreSidecar({ MACHINE_CORE_MODE: ' goa ' })).toBe(true);
        expect(shouldUseGoMachineCoreSidecar({ MACHINE_CORE_MODE: 'legacy' })).toBe(false);
    });

    test('supports per-path sidecar flags with safe defaults', () => {
        expect(shouldUseGoMachineCorePath('file_shadow', { MACHINE_CORE_TRANSPORT: 'grpc' })).toBe(true);
        expect(shouldUseGoMachineCorePath('file_shadow', {
            MACHINE_CORE_TRANSPORT: 'grpc',
            MACHINE_CORE_FILE_SHADOW_ENABLED: 'false',
        })).toBe(false);
        expect(shouldUseGoMachineCorePath('job_shadow', {
            MACHINE_CORE_MODE: 'goa',
            MACHINE_CORE_JOB_SHADOW_ENABLED: '0',
        })).toBe(false);
    });

    test('extracts session id from Goa openSession response', () => {
        expect(extractSessionID({ session: { id: 'session-1' } })).toBe('session-1');
        expect(extractSessionID({})).toBeNull();
    });

    test('emits replay event on backend socket channel', () => {
        const socket = {
            emit: jest.fn(),
        };
        const event = { type: 'job_started', sequence: 4 };

        emitReplayEventToSocket(socket, event);

        expect(socket.emit).toHaveBeenCalledWith('machine:session:event', event);
    });

    test('maps machine session events to legacy workflow and sender status', () => {
        expect(buildLegacyWorkflowState({ type: 'job_started' })).toBe('running');
        expect(buildLegacyWorkflowState({ type: 'job_paused' })).toBe('paused');
        expect(buildLegacyWorkflowState({ type: 'file_unloaded' })).toBe('idle');
        expect(buildLegacySenderStatus({ type: 'job_resumed' })).toEqual({
            workflowState: 'running',
            active: true,
        });
        expect(buildLegacyHomingState({ homing_required: true, has_homed: false })).toEqual({
            homingRequired: true,
            hasHomed: false,
        });
        expect(extractControllerType({ controller_type: 'grblhal' })).toBe('grblhal');
    });

    test('builds canonical replay events with loaded file data when available', () => {
        expect(buildCanonicalReplayEvent({
            type: 'file_loaded',
            sequence: 3,
        }, {
            gcode: 'G0 X0 Y0',
            meta: { size: 9, name: 'part.nc', visualizer: 'primary' },
        })).toEqual({
            type: 'file_loaded',
            sequence: 3,
            payload: {
                file: {
                    content: 'G0 X0 Y0',
                    size: 9,
                    name: 'part.nc',
                    visualizer: 'primary',
                },
            },
        });
    });

    test('relays supported replay events to canonical socket events', () => {
        const socket = {
            emit: jest.fn(),
        };
        const engine = {
            gcode: 'G0 X0 Y0',
            meta: { size: 9, name: 'part.nc', visualizer: 'primary' },
        };

        relayMachineSessionEvent(socket, { type: 'file_loaded', sequence: 3 }, engine);
        expect(socket.emit).toHaveBeenCalledWith('machine:session:event', {
            type: 'file_loaded',
            sequence: 3,
            payload: {
                file: {
                    content: 'G0 X0 Y0',
                    size: 9,
                    name: 'part.nc',
                    visualizer: 'primary',
                },
            },
        });
    });

    test('builds canonical snapshots with loaded file data when available', () => {
        expect(buildCanonicalSnapshot({
            session: {
                device_id: '/dev/ttyUSB0',
            },
            loaded_file: { name: 'part.nc' },
        }, {
            gcode: 'G0 X0 Y0',
            meta: { size: 9, name: 'part.nc', visualizer: 'primary' },
        })).toEqual({
            session: {
                device_id: '/dev/ttyUSB0',
            },
            loaded_file: {
                name: 'part.nc',
                content: 'G0 X0 Y0',
                size: 9,
                visualizer: 'primary',
            },
        });
    });

    test('relays session snapshot into canonical socket state events', () => {
        const socket = {
            emit: jest.fn(),
        };
        const engine = {
            gcode: 'G0 X0 Y0',
            meta: { size: 9, name: 'part.nc', visualizer: 'primary' },
        };

        relayMachineSessionSnapshot(socket, {
            session: {
                device_id: '/dev/ttyUSB0',
                controller_type: 'grblhal',
                workflow_state: 'paused',
            },
            controller_settings: { baud_rate: 115200 },
            controller_state: { connection_state: 'connected' },
            sender_status: { active: true },
            feeder_status: { queue: 2 },
            homing_state: { homing_required: true, has_homed: false },
            loaded_file: { name: 'part.nc' },
        }, engine);

        expect(socket.emit).toHaveBeenCalledWith('machine:session:snapshot', {
            session: {
                device_id: '/dev/ttyUSB0',
                controller_type: 'grblhal',
                workflow_state: 'paused',
            },
            controller_settings: { baud_rate: 115200 },
            controller_state: { connection_state: 'connected' },
            sender_status: { active: true },
            feeder_status: { queue: 2 },
            homing_state: { homing_required: true, has_homed: false },
            loaded_file: {
                name: 'part.nc',
                content: 'G0 X0 Y0',
                size: 9,
                visualizer: 'primary',
            },
        });
    });
});
