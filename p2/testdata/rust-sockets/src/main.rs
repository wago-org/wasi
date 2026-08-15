use std::io;
use std::net::{IpAddr, Ipv4Addr, SocketAddr, TcpStream, ToSocketAddrs, UdpSocket};

fn main() {
    let address = SocketAddr::new(IpAddr::V4(Ipv4Addr::LOCALHOST), 9);
    let err = TcpStream::connect(address).unwrap_err();
    assert_eq!(err.kind(), io::ErrorKind::PermissionDenied);

    let err = UdpSocket::bind(address).unwrap_err();
    assert_eq!(err.kind(), io::ErrorKind::PermissionDenied);

    let err = ("example.invalid", 80).to_socket_addrs().unwrap_err();
    assert_eq!(err.kind(), io::ErrorKind::PermissionDenied);

    println!("tcp=permission-denied;udp=permission-denied;dns=permission-denied");
}
