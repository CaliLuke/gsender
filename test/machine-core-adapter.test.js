import { test } from 'tap';
import machineCore, { setAdapter } from '../src/server/services/machine-core';

test('machine-core adapter dispatches to active backend', (t) => {
    const events = [];
    const fakeAdapter = {
        start: (server, controller) => {
            events.push({ method: 'start', server, controller });
        },
        stop: () => {
            events.push({ method: 'stop' });
        },
        load: (payload) => {
            events.push({ method: 'load', payload });
        },
        unload: () => {
            events.push({ method: 'unload' });
        },
    };

    setAdapter(fakeAdapter);
    t.teardown(() => {
        setAdapter();
    });

    const gcodePayload = { gcode: 'G0 X0 Y0', port: '/dev/ttyUSB0' };
    machineCore.start('srv', 'GRBL');
    machineCore.stop();
    machineCore.load(gcodePayload);
    machineCore.unload();

    t.same(events, [
        { method: 'start', server: 'srv', controller: 'GRBL' },
        { method: 'stop' },
        { method: 'load', payload: gcodePayload },
        { method: 'unload' }
    ]);

    t.end();
});

test('machine-core legacy adapter exposes session API as dispatchable no-op stubs', (t) => {
    const events = [];
    const fakeAdapter = {
        start: () => events.push('start'),
        stop: () => events.push('stop'),
        load: () => events.push('load'),
        unload: () => events.push('unload'),
        listDevices: () => {
            events.push('listDevices');
            return [{ id: 'sim://loopback' }];
        },
        openSession: () => {
            events.push('openSession');
            return { session_id: 'session-1' };
        },
        closeSession: () => {
            events.push('closeSession');
            return { ok: true };
        },
        getSnapshot: () => {
            events.push('getSnapshot');
            return { session: { id: 'session-1' } };
        },
        resolveSession: () => {
            events.push('resolveSession');
            return { session: { id: 'session-1' } };
        },
        loadFile: () => {
            events.push('loadFile');
            return { session: { id: 'session-1' } };
        },
        unloadFile: () => {
            events.push('unloadFile');
            return { ok: true };
        },
        startJob: () => {
            events.push('startJob');
            return { accepted: true };
        },
        pauseJob: () => {
            events.push('pauseJob');
            return { accepted: true };
        },
        resumeJob: () => {
            events.push('resumeJob');
            return { accepted: true };
        },
        stopJob: () => {
            events.push('stopJob');
            return { accepted: true };
        },
        attachClient: () => {
            events.push('attachClient');
            return { ok: true };
        },
        replayEvents: () => {
            events.push('replayEvents');
            return [{ type: 'session_opened' }];
        },
        detachClient: () => {
            events.push('detachClient');
            return { ok: true };
        },
        sendCommand: () => {
            events.push('sendCommand');
            return { accepted: true };
        },
    };

    setAdapter(fakeAdapter);
    t.teardown(() => {
        setAdapter();
    });

    const deviceList = machineCore.listDevices();
    const session = machineCore.openSession({ device_id: 'sim://loopback' });
    machineCore.closeSession('session-1');
    machineCore.getSnapshot('session-1');
    machineCore.resolveSession('sim://loopback', 'ui-1');
    machineCore.loadFile('session-1', { name: 'part.nc', gcode: 'G1 X10' });
    machineCore.unloadFile('session-1');
    machineCore.startJob('session-1');
    machineCore.pauseJob('session-1');
    machineCore.resumeJob('session-1');
    machineCore.stopJob('session-1', { force: true });
    machineCore.attachClient('session-1', 'ui-1');
    machineCore.replayEvents('session-1', 'ui-1');
    machineCore.detachClient('session-1', 'ui-1');
    machineCore.sendCommand('session-1', { type: 'status_report' });

    t.same(deviceList, [{ id: 'sim://loopback' }]);
    t.equal(session.session_id, 'session-1');
    t.same(events, [
        'listDevices',
        'openSession',
        'closeSession',
        'getSnapshot',
        'resolveSession',
        'loadFile',
        'unloadFile',
        'startJob',
        'pauseJob',
        'resumeJob',
        'stopJob',
        'attachClient',
        'replayEvents',
        'detachClient',
        'sendCommand',
    ]);

    t.end();
});
