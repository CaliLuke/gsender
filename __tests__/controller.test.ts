/* eslint-env jest */

describe('app/lib/controller', () => {
    const buildSocket = () => {
        const handlers: Record<string, (...args: any[]) => void> = {};
        const socket = {
            connected: true,
            on: jest.fn((event: string, handler: (...args: any[]) => void) => {
                handlers[event] = handler;
            }),
            emit: jest.fn(),
            connect: jest.fn(),
            disconnect: jest.fn(),
            handlers,
        };
        socket.connect.mockReturnValue(socket);
        return socket;
    };

    it('tracks Go-relayed lifecycle state and reconnects with the active port', () => {
        jest.resetModules();

        const sockets = [buildSocket(), buildSocket()];
        const io = jest.fn(() => sockets.shift());

        jest.doMock('socket.io-client', () => ({
            __esModule: true,
            default: io,
        }));
        jest.doMock('is-electron', () => ({
            __esModule: true,
            default: jest.fn(() => false),
        }));

        const controller = require('../src/app/src/lib/controller').default;

        const next = jest.fn();
        controller.connect('http://localhost:8000', { transports: ['websocket'] }, next);

        const firstSocket = io.mock.results[0].value;
        firstSocket.handlers.startup({
            loadedControllers: ['Grbl'],
            baudrates: [115200],
            ports: ['/dev/ttyUSB0'],
        });

        expect(next).toHaveBeenCalledTimes(1);
        expect(firstSocket.emit).toHaveBeenCalledWith('newConnection');

        firstSocket.handlers['machine:session:snapshot']({
            session: {
                device_id: '/dev/ttyUSB0',
                controller_type: 'grblHAL',
                workflow_state: 'running',
            },
            controller_settings: { settings: { $22: '1' } },
            controller_state: {
                status: { activeState: 'Run' },
            },
        });

        expect(controller.port).toBe('/dev/ttyUSB0');
        expect(controller.type).toBe('grblHAL');
        expect(controller.settings).toEqual({ settings: { $22: '1' } });
        expect(controller.state).toEqual({ status: { activeState: 'Run' } });
        expect(controller.workflow.state).toBe('running');

        firstSocket.handlers['machine:session:event']({
            type: 'session_closed',
            payload: {
                device_id: '/dev/ttyUSB0',
            },
        });

        expect(controller.port).toBe('');
        expect(controller.type).toBe('');
        expect(controller.settings).toEqual({});
        expect(controller.state).toEqual({});
        expect(controller.workflow.state).toBe('idle');

        firstSocket.handlers['machine:session:snapshot']({
            session: {
                device_id: '/dev/ttyUSB0',
                controller_type: 'grblHAL',
                workflow_state: 'running',
            },
            controller_settings: { settings: { $22: '1' } },
            controller_state: {
                status: { activeState: 'Run' },
            },
        });

        controller.reconnect();

        const secondSocket = io.mock.results[1].value;
        expect(secondSocket.emit).toHaveBeenCalledWith('reconnect', '/dev/ttyUSB0');

        secondSocket.handlers['machine:session:event']({
            type: 'session_closed',
            payload: { device_id: '/dev/ttyUSB0' },
        });

        expect(controller.port).toBe('');
        expect(controller.type).toBe('');
        expect(controller.settings).toEqual({});
        expect(controller.state).toEqual({});
        expect(controller.workflow.state).toBe('idle');
    });
});
