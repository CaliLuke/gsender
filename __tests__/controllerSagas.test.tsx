/* eslint-env jest */

describe('controller sagas frontend event wiring', () => {
    let controllerListeners: Record<string, (...args: any[]) => void>;
    let dispatch: jest.Mock;
    let getState: jest.Mock;
    let controllerMock: { addListener: jest.Mock; command: jest.Mock; healthCheck: jest.Mock };
    let storeMock: { get: jest.Mock; set: jest.Mock; emit: jest.Mock };
    let pubsubMock: { publish: jest.Mock; subscribe: jest.Mock };
    let workerPostMessage: jest.Mock;
    let setActiveVisualizeJobId: jest.Mock;

    beforeEach(() => {
        jest.resetModules();
        jest.useFakeTimers();

        controllerListeners = {};
        dispatch = jest.fn();
        getState = jest.fn(() => ({
            controller: {
                type: 'grblHAL',
                settings: {
                    settings: {},
                    info: {
                        NEWOPT: {
                            ATC: '0',
                        },
                    },
                },
                state: {
                    status: {
                        activeState: 'Idle',
                    },
                },
            },
            file: {
                renderState: 'no-file',
                content: '',
                size: 0,
                name: '',
                path: '/tmp/job.nc',
            },
            connection: {
                port: '/dev/ttyUSB0',
            },
        }));
        controllerMock = {
            addListener: jest.fn((eventName, handler) => {
                controllerListeners[eventName] = handler;
            }),
            command: jest.fn(),
            healthCheck: jest.fn(),
        };
        storeMock = {
            get: jest.fn((key: string, fallback?: any) => {
                const values: Record<string, any> = {
                    'widgets.visualizer.liteOption': 'light',
                    'widgets.visualizer.debug.profileWorker': false,
                    'widgets.visualizer.debug.profileSampleEvery': 10000,
                    'widgets.visualizer.rotaryDiameterOffsetEnabled': false,
                    'widgets.visualizer.showLineWarnings': false,
                    'widgets.spindle.delay': undefined,
                    'workspace.rotaryAxis.useAaxisForGrbl': false,
                    'workspace.machineProfile': null,
                    'workspace.revertWorkspace': false,
                };
                return Object.prototype.hasOwnProperty.call(values, key)
                    ? values[key]
                    : fallback;
            }),
            set: jest.fn(),
            emit: jest.fn(),
        };
        pubsubMock = {
            publish: jest.fn(),
            subscribe: jest.fn(),
        };
        workerPostMessage = jest.fn();
        setActiveVisualizeJobId = jest.fn();

        (global as any).Worker = jest.fn(() => ({
            postMessage: workerPostMessage,
            terminate: jest.fn(),
            onmessage: null,
        }));

        jest.doMock('app/lib/controller', () => ({
            __esModule: true,
            default: controllerMock,
        }));
        jest.doMock('app/store', () => ({
            __esModule: true,
            default: storeMock,
        }));
        jest.doMock('app/store/redux', () => ({
            store: {
                dispatch,
                getState,
            },
        }));
        jest.doMock('pubsub-js', () => ({
            __esModule: true,
            default: pubsubMock,
        }));
        jest.doMock('is-electron', () => ({
            __esModule: true,
            default: jest.fn(() => false),
        }));
        jest.doMock('app/components/ConfirmationDialog/ConfirmationDialogLib', () => ({
            Confirm: jest.fn(),
        }));
        jest.doMock('app/workers/Visualize.worker', () => ({
            __esModule: true,
            default: {},
        }));
        jest.doMock('app/workers/Visualize.response', () => ({
            setActiveVisualizeJobId,
            shouldVisualize: jest.fn(() => false),
            visualizeResponse: jest.fn(),
        }));
        jest.doMock('app/lib/laserMode', () => ({
            isLaserMode: jest.fn(() => false),
        }));
        jest.doMock('app/lib/getVisualizerTheme', () => ({
            getVisualizerTheme: jest.fn(() => 'light'),
        }));
        jest.doMock('app/api', () => ({
            __esModule: true,
            default: {
                jobStats: {
                    fetch: jest.fn(async () => ({ data: { jobs: [] } })),
                    update: jest.fn(),
                },
                maintenance: {
                    fetch: jest.fn(async () => ({ data: [] })),
                    update: jest.fn(),
                },
                alarmList: {
                    fetch: jest.fn(async () => ({ data: { list: [] } })),
                    update: jest.fn(),
                },
            },
        }));
        jest.doMock('app/lib/toaster', () => ({
            toast: {
                info: jest.fn(),
                success: jest.fn(),
            },
        }));
        jest.doMock('app/lib/connection', () => ({
            connectToLastDevice: jest.fn(),
        }));
        jest.doMock('app/lib/rotary', () => ({
            updateWorkspaceMode: jest.fn(),
        }));
        jest.doMock('app/features/Helper/Wizard.tsx', () => ({
            updateToolchangeContext: jest.fn(),
        }));
        jest.doMock('app/wizards/manualToolchange', () => jest.fn());
        jest.doMock('app/wizards/semiautoToolchange', () => jest.fn());
        jest.doMock('app/wizards/automaticToolchange', () => jest.fn());
        jest.doMock('app/wizards/semiautoToolchangeSecondRun', () => jest.fn());
        jest.doMock(
            'app/features/ATC/components/KeepOut/KeepOutToggle.tsx',
            () => ({
                KeepoutToggle: () => null,
            }),
        );
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    it('routes Go-relayed connect, file, workflow, and close events into Redux state', () => {
        const { initialize } = require('../src/app/src/store/redux/sagas/controllerSagas.tsx');
        const saga = initialize();
        saga.next();

        expect(Object.keys(controllerListeners)).toEqual(
            expect.arrayContaining([
                'serialport:open',
                'serialport:openController',
                'controller:settings',
                'controller:state',
                'feeder:status',
                'sender:status',
                'workflow:state',
                'file:load',
                'homing:has-homed',
                'job:start',
                'job:stop',
                'serialport:close',
            ]),
        );

        controllerListeners['serialport:open']({
            port: '/dev/ttyUSB0',
            baudrate: '115200',
            controllerType: 'grblHAL',
        });
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'connection/openConnection',
                payload: {
                    port: '/dev/ttyUSB0',
                    baudrate: '115200',
                    isConnected: false,
                },
            }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateControllerType',
                payload: { type: 'grblHAL' },
            }),
        );

        controllerListeners['serialport:openController']('grblHAL');
        jest.advanceTimersByTime(300);
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({ type: 'controller/clearSpindles' }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({ type: 'controller/resetHoming' }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'connection/setConnectionState',
                payload: { isConnected: true },
            }),
        );

        controllerListeners['controller:settings']('grblHAL', { settings: { $22: '1' } });
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateControllerSettings',
                payload: {
                    type: 'grblHAL',
                    settings: { settings: { $22: '1' } },
                },
            }),
        );

        controllerListeners['controller:state']('grblHAL', {
            status: { activeState: 'Run' },
            parserstate: { modal: {} },
        });
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateControllerState',
                payload: {
                    type: 'grblHAL',
                    state: {
                        status: { activeState: 'Run' },
                        parserstate: { modal: {} },
                    },
                },
            }),
        );

        controllerListeners['workflow:state']('running');
        controllerListeners['workflow:state']('paused');
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateWorkflowState',
                payload: { state: 'running' },
            }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateWorkflowState',
                payload: { state: 'paused' },
            }),
        );

        controllerListeners['sender:status']({
            sent: 1,
            finishTime: 0,
            elapsedTime: 0,
        });
        controllerListeners['feeder:status']({ queueSize: 1 });
        controllerListeners['homing:has-homed'](true);
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateSenderStatus',
                payload: {
                    status: {
                        sent: 1,
                        finishTime: 0,
                        elapsedTime: 0,
                    },
                },
            }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateFeederStatus',
                payload: { status: { queueSize: 1 } },
            }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'controller/updateHasHomed',
                payload: { hasHomed: true },
            }),
        );

        controllerListeners['file:load']('G1 X1', 5, 'job.nc', 'primary');
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'fileInfo/updateFileContent',
                payload: {
                    content: 'G1 X1',
                    size: 5,
                    name: 'job.nc',
                },
            }),
        );
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'fileInfo/updateFileProcessing',
                payload: { fileProcessing: true },
            }),
        );
        expect(pubsubMock.publish).toHaveBeenCalledWith('file:content', {
            content: 'G1 X1',
            size: 5,
            name: 'job.nc',
        });
        expect(setActiveVisualizeJobId).toHaveBeenCalledWith(1);
        expect(workerPostMessage).toHaveBeenCalledWith(
            expect.objectContaining({
                content: 'G1 X1',
                visualizer: 'primary',
                jobId: 1,
            }),
        );

        controllerListeners['serialport:close']({ port: '/dev/ttyUSB0' }, 0);
        expect(dispatch).toHaveBeenCalledWith(
            expect.objectContaining({
                type: 'connection/closeConnection',
                payload: { port: '/dev/ttyUSB0' },
            }),
        );
    });

    it('keeps the frontend stop flow compatible with replayed job state', () => {
        const { initialize } = require('../src/app/src/store/redux/sagas/controllerSagas.tsx');
        const saga = initialize();
        saga.next();

        controllerListeners['job:start']();
        controllerListeners['job:stop']();

        expect(controllerMock.command).toHaveBeenCalledWith(
            'gcode',
            '[global.state.workspace]',
        );
    });
});
