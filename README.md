

### gsnova 0.31.0 修改版，用于openshift v3 docker部署  


#### 修改项  

目标：规避openshift代理检查等  
基准：官方 0.32.0 commit d38d772 Mar 17,2018  
- 版本号r16_v32_M23G2 docker 0318  
- 修改pid文件名  
- 随机证书改为2048bit(1024)  
- client模式时，显示心跳包延迟时间  
- 仅在client模式时显示ASCIILogo，server模式时不显示  
- server端默认Mux.IdleTimeout改为1800(300)，即使无数据也保持长连接30分钟  
- 加大默认的Mux的MaxStreamWindow和StreamMinRefresh为原有4倍，即2048k和128k  
- 增加key的环境变量AVONSG_CIPHER_KEY，也保留原有GSNOVA_CIPHER_KEY，规避检查需要  
- remote.indexCallback改为http.StatusOK,即去掉原有https访问时的版本提示，规避检查需要  
- 加入logger.Printf，修改所有包log.Printf为logger调用  
- 修改logger包，加入none及null选项，便于server端命令行模式时，使用-log none关闭所有提示  
- 修正loadGFWList长时间不释放https连接  
- 增加环境变量AVONSG_CIPHER_USER，仅用于服务端鉴权，优先级高于命令行和json  
- 修正AllowUsers鉴权失效问题
- 同步官方cd936c6,增加HibernateAfterSecs参数，客户端默认30分钟无数据时关闭muxSession    


#### Thanks : yinqiwen  
<https://github.com/yinqiwen/gsnova>  
  
  
  

