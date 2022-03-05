
# pong-go

- [中文](README.md)
- [English](readme_en.md)



pong-go 是 pong 代理协议的 golang 语言开源实现。



## 安装

- linux shell
  
    curl -o- -L https://github.com/pingworlds/pong-install//releases/latest/download/install.sh | bash

  如果 shell 无法正确执行，请尝试下面的方法。


- 发行版

  下载最新编译版 <https://github.com/pingworlds/pong/releases>

- 源代码

  直接下载源代码

- 配置指令
  
  请参考指南  <https://github.com/pingworlds/pong-install>


- aar

作为android客户端的第三方库使用时,使用 gomobile bind 命令编译成 aar, 参考 [gomobile.txt](gomobile.txt)




## transport protocols

pong-go 支持以下标准协议作为传输协议：
- tcp
- tls
- http
- https
- http2
- h2c
- http3
- ws
- wss


## proxy protocols

pong-go 同时支持以下代理协议：
- pong
- shadowsokcs 
- vless
- socks5
- qsocks (没有握手过程的精简版socks5)

注意：所有代理协议，仅支持明文


## 关联项目

- pong-protocol 
  
  <https://github.com/pingworlds/pong-protocol>

- pong-go install  
  
  <https://github.com/pingworlds/pong-install>
  
- pong-go install example 
  
  <https://github.com/pingworlds/pong-config-example>


- ping 
  
  pong protocol android app <https://github.com/pingworlds/ping>



## pong proxy protocol

pong 是一种结合了socks5与http2特性的代理协议，支持在一条网络连接上多路复用同时并发数十数百个代理请求。

pong协议的优势：

- 避免“连接阻塞”
 
因为安全和隐私保护需要，代理服务通常放置在web server之后，

    src <---> proxy-local <---> web server <---> proxy server <---> dst 

web server对单一客户端有着最大并发连接数限制，一般不会超过10，打开一个新闻/图片网站首页往往需要并发50-100个代理请求，连接数耗尽瞬间耗尽，后续代理请求排队等待连接释放，于是发生客户端卡顿假死，通常只能重启客户端来强制释放连接。

在90%以上的时间里，一个pong客户端只需要维持一个网络连接，可以有效避免“连接阻塞”导致的卡顿。
  
- 0 open 时间 
  
因为是在一条已经打开的网络连接上并发代理，无需耗费时间去为每个代理请求打开网络连接和握手验证，收到代理请求会立即转发，速度上pong会优于其它协议。

关于pong协议的细节，请参阅 <https://github.com/pingworlds/pong-protocol>


 