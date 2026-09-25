#[cfg(unix)]
pub(crate) use std::os::unix::net::UnixStream;

#[cfg(windows)]
mod windows_stream {
    use std::ffi::c_void;
    use std::io::{self, Read, Write};
    use std::sync::{Arc, OnceLock};

    const AF_UNIX: i32 = 1;
    const SOCK_STREAM: i32 = 1;
    const INVALID_SOCKET: usize = usize::MAX;
    const SOCKET_ERROR: i32 = -1;
    const UNIX_PATH_MAX: usize = 108;

    #[repr(C)]
    struct SockAddrUnix {
        family: u16,
        path: [u8; UNIX_PATH_MAX],
    }

    #[link(name = "Ws2_32")]
    unsafe extern "system" {
        fn WSAStartup(version: u16, data: *mut c_void) -> i32;
        fn WSAGetLastError() -> i32;
        fn socket(address_family: i32, socket_type: i32, protocol: i32) -> usize;
        fn connect(socket: usize, address: *const SockAddrUnix, address_length: i32) -> i32;
        fn recv(socket: usize, buffer: *mut u8, length: i32, flags: i32) -> i32;
        fn send(socket: usize, buffer: *const u8, length: i32, flags: i32) -> i32;
        fn closesocket(socket: usize) -> i32;
    }

    fn last_socket_error() -> io::Error {
        io::Error::from_raw_os_error(unsafe { WSAGetLastError() })
    }

    fn initialize_winsock() -> io::Result<()> {
        static STARTUP: OnceLock<Result<(), i32>> = OnceLock::new();
        match STARTUP.get_or_init(|| {
            // WSADATA is 408 bytes on supported 64-bit Windows targets. Keep
            // extra aligned storage so this stays valid if the SDK grows it.
            let mut data = [0usize; 64];
            let result = unsafe { WSAStartup(0x0202, data.as_mut_ptr().cast()) };
            if result == 0 { Ok(()) } else { Err(result) }
        }) {
            Ok(()) => Ok(()),
            Err(code) => Err(io::Error::from_raw_os_error(*code)),
        }
    }

    struct Socket(usize);

    impl Drop for Socket {
        fn drop(&mut self) {
            unsafe { closesocket(self.0) };
        }
    }

    pub(crate) struct UnixStream {
        socket: Arc<Socket>,
    }

    impl UnixStream {
        pub(crate) fn connect(path: &str) -> io::Result<Self> {
            initialize_winsock()?;
            let bytes = path.as_bytes();
            if bytes.is_empty() || bytes.len() >= UNIX_PATH_MAX || bytes.contains(&0) {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidInput,
                    format!("invalid Windows AF_UNIX path length {}", bytes.len()),
                ));
            }
            let raw = unsafe { socket(AF_UNIX, SOCK_STREAM, 0) };
            if raw == INVALID_SOCKET {
                return Err(last_socket_error());
            }
            let socket = Socket(raw);
            let mut address = SockAddrUnix {
                family: AF_UNIX as u16,
                path: [0; UNIX_PATH_MAX],
            };
            address.path[..bytes.len()].copy_from_slice(bytes);
            let length = (std::mem::size_of::<u16>() + bytes.len() + 1) as i32;
            if unsafe { connect(raw, &address, length) } == SOCKET_ERROR {
                return Err(last_socket_error());
            }
            Ok(Self {
                socket: Arc::new(socket),
            })
        }

        pub(crate) fn try_clone(&self) -> io::Result<Self> {
            Ok(Self {
                socket: self.socket.clone(),
            })
        }
    }

    impl Read for UnixStream {
        fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
            let length = buffer.len().min(i32::MAX as usize) as i32;
            let result = unsafe { recv(self.socket.0, buffer.as_mut_ptr(), length, 0) };
            if result == SOCKET_ERROR {
                Err(last_socket_error())
            } else {
                Ok(result as usize)
            }
        }
    }

    impl Write for UnixStream {
        fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
            let length = buffer.len().min(i32::MAX as usize) as i32;
            let result = unsafe { send(self.socket.0, buffer.as_ptr(), length, 0) };
            if result == SOCKET_ERROR {
                Err(last_socket_error())
            } else {
                Ok(result as usize)
            }
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
}

#[cfg(windows)]
pub(crate) use windows_stream::UnixStream;
